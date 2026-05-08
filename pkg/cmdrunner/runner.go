package cmdrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// MaxOutputBytes caps stdout and stderr capture per invocation.  64 KB
// is generous for the contract (one runID line, one JSON status doc)
// and small enough that a misbehaving executor flooding output cannot
// OOM the controller.  Excess bytes are dropped silently and
// RunResult.Truncated reflects this.
const MaxOutputBytes = 64 * 1024

// inheritedEnvKeys is the strict allowlist of host-environment keys
// passed through to every executor invocation.  We deliberately do NOT
// inherit os.Environ() wholesale: it leaks operator-side debug env
// (KUBECONFIG, AWS_*, GCS_*, …) and credential helpers that the
// executor program almost never wants.  Anything else the operator
// needs goes in Spec.Env or Spec.SecretEnv.
//
// The list is intentionally small — anything added must be justified
// case-by-case.  PATH lets the executor find subprograms; HOME/USER
// satisfy tools that look these up; LANG/LC_ALL/TZ keep human-readable
// output predictable.
var inheritedEnvKeys = []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TZ"}

// RunSpec describes a single invocation — one dispatch call or one
// status call.  The runner is contract-agnostic at this level: it
// doesn't know which the caller is doing.  Contract-aware helpers
// (ExtractRunID, ParseRunStatus) live alongside in this package.
type RunSpec struct {
	// Argv is the executable + arguments.  argv[0] should be an
	// absolute path; the runner does not perform PATH lookup beyond
	// what the OS does at exec(2) time.
	Argv []string

	// Stdin is delivered to the subprocess's stdin and EOF'd.  Empty
	// is allowed.
	Stdin []byte

	// Env is the literal environment from PoudriereBulk.Spec.Executor.
	// Command.Env (or wherever the caller composes it from).  Keys
	// here override inherited host env; SecretEnv overrides both.
	Env []corev1.EnvVar

	// SecretEnv resolves Secret keys to env vars at every Run() call.
	// Resolved values never enter the runner's logs or error messages.
	SecretEnv []freebsdv1.CommandSecretEnv

	// Namespace scopes Secret lookups.  Required when SecretEnv is
	// non-empty.
	Namespace string

	// Timeout caps the subprocess.  Zero means no timeout (caller's
	// context.Context still bounds it).
	Timeout time.Duration
}

// RunResult captures everything the caller needs to interpret one
// invocation's outcome.  Always populated — even on non-zero exit —
// so the caller can surface stderr into Status.LastError.
type RunResult struct {
	// Stdout is captured up to MaxOutputBytes.  Bytes beyond that
	// limit are dropped and Truncated is set.
	Stdout []byte

	// Stderr is captured up to MaxOutputBytes (independent budget).
	Stderr []byte

	// ExitCode is the subprocess exit status.  -1 when the process
	// could not be started (e.g. argv[0] missing) or was terminated by
	// a signal.
	ExitCode int

	// Truncated is true when either stdout or stderr exceeded
	// MaxOutputBytes and was clipped.
	Truncated bool
}

// Runner executes RunSpecs.  One Runner instance is intended to be
// shared across reconciles; it carries no per-invocation state.
type Runner struct {
	// client is used to resolve SecretEnv references.  Any
	// client.Reader works — the controller-runtime client during
	// production, a fake client in tests.
	client client.Reader

	// logger receives structured log lines about every invocation.
	// CRITICAL: this code path must NEVER log resolved secret values.
	// Tests in runner_test.go assert this property holds.
	logger *slog.Logger

	// hostEnv resolves an inherited env key to its value.  Defaults
	// to os.Getenv but overridable in tests so the no-leak suite can
	// run hermetically.
	hostEnv func(key string) string
}

// New constructs a Runner.  Both arguments are required.
func New(c client.Reader, logger *slog.Logger) *Runner {
	return &Runner{
		client:  c,
		logger:  logger,
		hostEnv: os.Getenv,
	}
}

// Run executes the RunSpec.  Returns RunResult (always populated) and
// an error only on infrastructural failures: empty argv, Secret
// resolution failure, or process-start failure.  A non-zero exit code
// from the subprocess is NOT an error from Run's perspective — it is
// reported via RunResult.ExitCode and the caller decides how to
// react.  This split lets callers distinguish "couldn't even try"
// from "tried, failed".
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if len(spec.Argv) == 0 {
		return RunResult{}, errors.New("cmdrunner.Run: empty argv")
	}

	// Resolve secrets BEFORE any logging so the structured log line
	// can reference sources but the values themselves never enter the
	// log scope.
	secretValues, secretSources, err := r.resolveSecretEnv(ctx, spec.Namespace, spec.SecretEnv)
	if err != nil {
		return RunResult{}, fmt.Errorf("cmdrunner.Run: %w", err)
	}

	env := r.composeEnv(spec.Env, secretValues)

	// envKeys is sorted for deterministic logging; the slice itself
	// contains only KEY=VALUE strings, but we log NAMES only.
	envKeys := envNames(env)

	// Apply per-invocation timeout if set.  Caller's ctx still
	// bounds it — whichever fires first wins.
	runCtx := ctx
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	r.logger.Info("running command",
		"program", spec.Argv[0],
		"argc", len(spec.Argv),
		"env_keys", envKeys,
		"secret_sources", secretSources,
		"timeout", spec.Timeout,
		"stdin_bytes", len(spec.Stdin),
	)

	// CommandContext sends SIGKILL when runCtx is done.  We deliberately
	// do not use cmd.WaitDelay (Go 1.20+) because we want the process
	// gone immediately on timeout — long-running poudriere bulk
	// executors should be canceled by the upstream system, not by us
	// dragging out their lifetime.
	cmd := exec.CommandContext(runCtx, spec.Argv[0], spec.Argv[1:]...) // #nosec G204 — argv comes from operator-controlled CR
	cmd.Env = env
	if len(spec.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}

	stdout := &cappedBuffer{cap: MaxOutputBytes}
	stderr := &cappedBuffer{cap: MaxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()

	res := RunResult{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		ExitCode:  exitCode(cmd, runErr),
		Truncated: stdout.truncated || stderr.truncated,
	}

	// Three outcomes to distinguish:
	//   1. Context cancelled or timed out → surface as Run error so the
	//      caller can react (don't update CR status as if a real run
	//      finished; instead retry next reconcile).
	//   2. Process-start failure (binary missing, EACCES, …) → Run error.
	//   3. Process ran and exited non-zero → NOT a Run error; the
	//      caller decides what to do based on RunResult.ExitCode and
	//      stderr.
	if cerr := runCtx.Err(); cerr != nil {
		return res, fmt.Errorf("cmdrunner.Run: %w", cerr)
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return res, fmt.Errorf("cmdrunner.Run: %w", runErr)
	}

	r.logger.Info("command finished",
		"program", spec.Argv[0],
		"exit_code", res.ExitCode,
		"stdout_bytes", len(res.Stdout),
		"stderr_bytes", len(res.Stderr),
		"truncated", res.Truncated,
	)
	return res, nil
}

// resolveSecretEnv reads each referenced Secret and returns a map of
// env-var-name → value plus a parallel list of human-readable source
// descriptors (e.g. "forgejo-poudriere-pat#token→FORGEJO_TOKEN") used
// only in log lines.  Values themselves never enter the source list.
//
// Multiple references to the same Secret use a single Get; the
// per-Secret cache is local to one call.
func (r *Runner) resolveSecretEnv(ctx context.Context, namespace string, refs []freebsdv1.CommandSecretEnv) (map[string]string, []string, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if namespace == "" {
		return nil, nil, errors.New("SecretEnv requires Namespace")
	}

	values := make(map[string]string, len(refs))
	sources := make([]string, 0, len(refs))
	cache := map[string]*corev1.Secret{}

	for _, ref := range refs {
		secretName := ref.SecretRef.Name
		sec, ok := cache[secretName]
		if !ok {
			sec = &corev1.Secret{}
			if err := r.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretName}, sec); err != nil {
				// Error message includes Secret name (operator-visible
				// metadata) but never the value (we haven't read it).
				return nil, nil, fmt.Errorf("get secret %s/%s: %w", namespace, secretName, err)
			}
			cache[secretName] = sec
		}

		val, present := sec.Data[ref.SecretRef.Key]
		if !present {
			return nil, nil, fmt.Errorf("secret %s/%s missing key %q", namespace, secretName, ref.SecretRef.Key)
		}

		values[ref.Name] = string(val)
		sources = append(sources, fmt.Sprintf("%s#%s→%s", secretName, ref.SecretRef.Key, ref.Name))
	}

	return values, sources, nil
}

// composeEnv builds the final []string env in K=V form.  Order of
// precedence (lowest to highest, later wins): host allowlist →
// Spec.Env → resolved SecretEnv.  This means an operator who sets the
// same key in both Spec.Env and Spec.SecretEnv gets the secret value;
// they almost certainly meant that.
func (r *Runner) composeEnv(literal []corev1.EnvVar, secrets map[string]string) []string {
	merged := make(map[string]string, len(inheritedEnvKeys)+len(literal)+len(secrets))

	for _, k := range inheritedEnvKeys {
		if v := r.hostEnv(k); v != "" {
			merged[k] = v
		}
	}
	for _, e := range literal {
		// We deliberately ignore EnvVar.ValueFrom — secret-via-env
		// is what SecretEnv is for.  ConfigMap fan-out and
		// FieldRef-style references aren't part of the contract.
		if e.Value != "" || e.ValueFrom == nil {
			merged[e.Name] = e.Value
		}
	}
	maps.Copy(merged, secrets)

	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	// Sort so ordering is deterministic (helps tests; harmless at
	// runtime since the OS doesn't care).
	sort.Strings(out)
	return out
}

// envNames returns the sorted list of env var names from a KEY=VALUE
// slice.  Used for structured logging — names are operator-visible
// metadata; values must NEVER appear in the log scope.
func envNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// exitCode extracts the subprocess exit status.  Returns -1 when the
// process couldn't be started, was killed by a signal, or the runtime
// otherwise failed to report a clean exit code.
func exitCode(cmd *exec.Cmd, err error) int {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return cmd.ProcessState.ExitCode() // 0 on clean success
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	default:
		return -1
	}
}

// cappedBuffer is a write-side buffer that drops bytes past `cap` and
// reports whether it ever truncated.  Avoids unbounded memory growth
// from a flooding executor.
type cappedBuffer struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.cap - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil // tell the writer we accepted everything; we just dropped it
	}
	if len(p) <= remaining {
		return b.buf.Write(p)
	}
	// Partial write to fill the buffer; drop the rest.
	n, err := b.buf.Write(p[:remaining])
	if err != nil {
		return n, err
	}
	b.truncated = true
	return len(p), nil
}

func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

// ExtractRunID returns the last non-empty line of stdout, trimmed of
// surrounding whitespace.  Implements the dispatch side of the
// contract: "stdout: trimmed last non-empty line, interpreted as an
// opaque runID".  Returns "" when stdout has no non-empty lines.
func ExtractRunID(stdout []byte) string {
	lines := bytes.Split(stdout, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(string(lines[i]))
		if s != "" {
			return s
		}
	}
	return ""
}

// ParseRunStatus unmarshals stdout as a BulkRunStatus and validates
// the contract discriminators.  Implements the status side: "stdout:
// one BulkRunStatus JSON object".  Unknown JSON fields are silently
// ignored per the forward-compatibility rule.
func ParseRunStatus(stdout []byte) (BulkRunStatus, error) {
	var s BulkRunStatus
	if err := json.Unmarshal(stdout, &s); err != nil {
		return s, fmt.Errorf("parse BulkRunStatus: %w", err)
	}
	if s.APIVersion != ContractAPIVersion {
		return s, fmt.Errorf("unsupported apiVersion %q (expected %q)", s.APIVersion, ContractAPIVersion)
	}
	if s.Kind != KindBulkRunStatus {
		return s, fmt.Errorf("unsupported kind %q (expected %q)", s.Kind, KindBulkRunStatus)
	}
	switch s.State {
	case BulkRunStateRunning, BulkRunStateSuccess, BulkRunStateFailed, BulkRunStateUnknown:
		// OK
	case "":
		return s, errors.New("BulkRunStatus.state is required")
	default:
		return s, fmt.Errorf("unsupported state %q", s.State)
	}
	return s, nil
}
