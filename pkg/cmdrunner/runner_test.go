package cmdrunner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// newTestRunner builds a Runner with a fake client and a slog handler
// that writes to a bytes.Buffer the test can inspect.  The hostEnv
// override returns "" for every key so tests are hermetic regardless
// of the test runner's environment.
func newTestRunner(t *testing.T, secrets ...*corev1.Secret) (*Runner, *bytes.Buffer) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	objs := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		objs = append(objs, s)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := New(c, logger)
	r.hostEnv = func(string) string { return "" }
	return r, logBuf
}

func TestRun_EmptyArgvIsRejected(t *testing.T) {
	r, _ := newTestRunner(t)
	_, err := r.Run(context.Background(), RunSpec{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty argv")
}

func TestRun_CapturesStdoutStderrAndExitCode(t *testing.T) {
	r, _ := newTestRunner(t)
	res, err := r.Run(context.Background(), RunSpec{
		Argv: []string{"/bin/sh", "-c", "echo hello-stdout; echo bye-stderr 1>&2; exit 3"},
	})
	require.NoError(t, err, "non-zero exit is not a Run error; only infra failures are")
	require.Equal(t, 3, res.ExitCode)
	require.Equal(t, "hello-stdout\n", string(res.Stdout))
	require.Equal(t, "bye-stderr\n", string(res.Stderr))
	require.False(t, res.Truncated)
}

func TestRun_StdinIsDelivered(t *testing.T) {
	r, _ := newTestRunner(t)
	res, err := r.Run(context.Background(), RunSpec{
		Argv:  []string{"/bin/sh", "-c", "cat"},
		Stdin: []byte(`{"hello":"world"}`),
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, `{"hello":"world"}`, string(res.Stdout))
}

func TestRun_TimeoutKillsSubprocess(t *testing.T) {
	r, _ := newTestRunner(t)
	start := time.Now()
	res, err := r.Run(context.Background(), RunSpec{
		Argv:    []string{"/bin/sh", "-c", "sleep 60"},
		Timeout: 200 * time.Millisecond,
	})
	elapsed := time.Since(start)
	require.Error(t, err, "timeout fires as a Run error (process killed)")
	require.Less(t, elapsed, 5*time.Second, "subprocess must die promptly on timeout")
	require.Equal(t, -1, res.ExitCode, "killed-by-signal exit is reported as -1")
}

func TestRun_ContextCancellationKillsSubprocess(t *testing.T) {
	r, _ := newTestRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := r.Run(ctx, RunSpec{
		Argv: []string{"/bin/sh", "-c", "sleep 60"},
	})
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestRun_StdoutTruncationIsReported(t *testing.T) {
	r, _ := newTestRunner(t)
	// 200 KB of "x" — well past the 64 KB cap.
	res, err := r.Run(context.Background(), RunSpec{
		Argv: []string{"/bin/sh", "-c", "printf 'x%.0s' $(seq 1 200000)"},
	})
	require.NoError(t, err)
	require.True(t, res.Truncated)
	require.LessOrEqual(t, len(res.Stdout), MaxOutputBytes)
}

func TestRun_LiteralEnvIsPassed(t *testing.T) {
	r, _ := newTestRunner(t)
	res, err := r.Run(context.Background(), RunSpec{
		Argv: []string{"/bin/sh", "-c", "echo $MY_VAR"},
		Env:  []corev1.EnvVar{{Name: "MY_VAR", Value: "from-spec"}},
	})
	require.NoError(t, err)
	require.Equal(t, "from-spec\n", string(res.Stdout))
}

func TestRun_HostEnvNotInheritedExceptAllowlist(t *testing.T) {
	r, _ := newTestRunner(t)
	// Override hostEnv to return values for both an allowlisted key
	// (PATH) and a non-allowlisted key (KUBECONFIG) — the script
	// should see PATH but not KUBECONFIG.
	r.hostEnv = func(k string) string {
		switch k {
		case "PATH":
			return "/usr/bin:/bin"
		case "KUBECONFIG":
			return "/secret/kubeconfig"
		default:
			return ""
		}
	}
	res, err := r.Run(context.Background(), RunSpec{
		Argv: []string{"/bin/sh", "-c", `echo "PATH=$PATH"; echo "KUBECONFIG=$KUBECONFIG"`},
	})
	require.NoError(t, err)
	require.Contains(t, string(res.Stdout), "PATH=/usr/bin:/bin")
	require.Contains(t, string(res.Stdout), "KUBECONFIG=", "key must appear with empty value because allowlist excludes it")
	require.NotContains(t, string(res.Stdout), "KUBECONFIG=/secret/kubeconfig",
		"non-allowlisted host env values must NOT leak to subprocess")
}

// TestRun_SecretEnvIsResolvedFromCluster is the happy-path: a Secret
// in the cluster, a CommandSecretEnv pointing at it, the value
// arrives in the subprocess's env.
func TestRun_SecretEnvIsResolvedFromCluster(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "forgejo-pat", Namespace: "nodemanager"},
		Data:       map[string][]byte{"token": []byte("super-secret-value")},
	}
	r, _ := newTestRunner(t, secret)

	res, err := r.Run(context.Background(), RunSpec{
		Argv:      []string{"/bin/sh", "-c", `echo "TOKEN=$FORGEJO_TOKEN"`},
		Namespace: "nodemanager",
		SecretEnv: []freebsdv1.CommandSecretEnv{{
			Name: "FORGEJO_TOKEN",
			SecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "forgejo-pat"},
				Key:                  "token",
			},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "TOKEN=super-secret-value\n", string(res.Stdout))
}

// TestRun_SecretValuesNeverAppearInLogs is the headline security
// regression test.  Resolve a secret, run a script that echoes its
// own env (the kind of thing a debugging operator might write), then
// assert the log buffer captured by the runner does NOT contain the
// secret's value.  Stdout DOES contain the value (the script chose to
// emit it) — that's the script author's call, not ours; what we
// guarantee is that nodemanager's structured logging never leaks it.
func TestRun_SecretValuesNeverAppearInLogs(t *testing.T) {
	const secretValue = "ULTRA-PRIVATE-SECRET-TOKEN-NEVER-LOG-THIS"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "private", Namespace: "nodemanager"},
		Data:       map[string][]byte{"token": []byte(secretValue)},
	}
	r, logBuf := newTestRunner(t, secret)

	_, err := r.Run(context.Background(), RunSpec{
		// The script reads the secret env var but doesn't echo it; we want to
		// be sure the runner itself doesn't leak it regardless of what the
		// script does.
		Argv:      []string{"/bin/sh", "-c", `: "$FORGEJO_TOKEN"; echo done`},
		Namespace: "nodemanager",
		SecretEnv: []freebsdv1.CommandSecretEnv{{
			Name: "FORGEJO_TOKEN",
			SecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "private"},
				Key:                  "token",
			},
		}},
	})
	require.NoError(t, err)
	require.NotContains(t, logBuf.String(), secretValue,
		"runner logs must NOT contain resolved secret values; if this fails review every fmt/slog call in runner.go for value leakage")
	// But the source descriptor IS allowed to be logged (it's metadata, not a value).
	require.Contains(t, logBuf.String(), "private#token→FORGEJO_TOKEN",
		"source descriptor should be logged so operators can debug which secret was used")
}

func TestRun_SecretEnvOverridesLiteralEnv(t *testing.T) {
	// Operator sets the same env name in both Spec.Env and Spec.SecretEnv.
	// Most-specific wins → secret value populates the env var.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "private", Namespace: "ns"},
		Data:       map[string][]byte{"k": []byte("from-secret")},
	}
	r, _ := newTestRunner(t, secret)
	res, err := r.Run(context.Background(), RunSpec{
		Argv:      []string{"/bin/sh", "-c", "echo $X"},
		Namespace: "ns",
		Env:       []corev1.EnvVar{{Name: "X", Value: "from-literal"}},
		SecretEnv: []freebsdv1.CommandSecretEnv{{
			Name: "X",
			SecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "private"},
				Key:                  "k",
			},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "from-secret\n", string(res.Stdout))
}

func TestRun_SecretEnvMissingSecretIsAnError(t *testing.T) {
	r, _ := newTestRunner(t)
	_, err := r.Run(context.Background(), RunSpec{
		Argv:      []string{"/bin/sh", "-c", "true"},
		Namespace: "ns",
		SecretEnv: []freebsdv1.CommandSecretEnv{{
			Name: "X",
			SecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "does-not-exist"},
				Key:                  "k",
			},
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ns/does-not-exist")
}

func TestRun_SecretEnvMissingKeyIsAnError(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data:       map[string][]byte{"actual": []byte("v")},
	}
	r, _ := newTestRunner(t, secret)
	_, err := r.Run(context.Background(), RunSpec{
		Argv:      []string{"/bin/sh", "-c", "true"},
		Namespace: "ns",
		SecretEnv: []freebsdv1.CommandSecretEnv{{
			Name: "X",
			SecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "s"},
				Key:                  "missing",
			},
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `missing key "missing"`)
}

func TestRun_SecretEnvWithoutNamespaceIsAnError(t *testing.T) {
	r, _ := newTestRunner(t)
	_, err := r.Run(context.Background(), RunSpec{
		Argv: []string{"/bin/sh", "-c", "true"},
		SecretEnv: []freebsdv1.CommandSecretEnv{{
			Name: "X",
			SecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "s"},
				Key:                  "k",
			},
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Namespace")
}

func TestRun_ProgramNotFoundIsAnError(t *testing.T) {
	r, _ := newTestRunner(t)
	_, err := r.Run(context.Background(), RunSpec{
		Argv: []string{"/this/path/does/not/exist/ever"},
	})
	require.Error(t, err)
}

func TestExtractRunID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single line", "run-123", "run-123"},
		{"trailing newline", "run-123\n", "run-123"},
		{"multiple lines, last wins", "first\nsecond\nthird\n", "third"},
		{"trailing empty lines skipped", "real-id\n\n\n", "real-id"},
		{"whitespace trimmed", "  run-123  \n", "run-123"},
		{"empty stdout returns empty", "", ""},
		{"only whitespace returns empty", "   \n\t\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ExtractRunID([]byte(tc.in)))
		})
	}
}

func TestParseRunStatus_Valid(t *testing.T) {
	in := `{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkRunStatus","state":"running","url":"https://x"}`
	got, err := ParseRunStatus([]byte(in))
	require.NoError(t, err)
	require.Equal(t, BulkRunStateRunning, got.State)
	require.Equal(t, "https://x", got.URL)
}

func TestParseRunStatus_RejectsWrongAPIVersion(t *testing.T) {
	in := `{"apiVersion":"freebsd.nodemanager/v0","kind":"BulkRunStatus","state":"success"}`
	_, err := ParseRunStatus([]byte(in))
	require.Error(t, err)
	require.Contains(t, err.Error(), "apiVersion")
}

func TestParseRunStatus_RejectsWrongKind(t *testing.T) {
	in := `{"apiVersion":"freebsd.nodemanager/v1","kind":"WrongKind","state":"success"}`
	_, err := ParseRunStatus([]byte(in))
	require.Error(t, err)
	require.Contains(t, err.Error(), "kind")
}

func TestParseRunStatus_RejectsMissingState(t *testing.T) {
	in := `{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkRunStatus"}`
	_, err := ParseRunStatus([]byte(in))
	require.Error(t, err)
	require.Contains(t, err.Error(), "state")
}

func TestParseRunStatus_RejectsUnknownState(t *testing.T) {
	in := `{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkRunStatus","state":"melting"}`
	_, err := ParseRunStatus([]byte(in))
	require.Error(t, err)
	require.Contains(t, err.Error(), "melting")
}

func TestParseRunStatus_RejectsMalformedJSON(t *testing.T) {
	_, err := ParseRunStatus([]byte("not json"))
	require.Error(t, err)
}

func TestParseRunStatus_IgnoresUnknownFields(t *testing.T) {
	in := `{
		"apiVersion":"freebsd.nodemanager/v1",
		"kind":"BulkRunStatus",
		"state":"success",
		"someFutureField":"with-value",
		"another":42
	}`
	got, err := ParseRunStatus([]byte(in))
	require.NoError(t, err)
	require.Equal(t, BulkRunStateSuccess, got.State)
}

func TestComposeEnv_OrderOfPrecedence(t *testing.T) {
	r, _ := newTestRunner(t)
	r.hostEnv = func(k string) string {
		if k == "PATH" {
			return "host-path"
		}
		return ""
	}
	env := r.composeEnv(
		[]corev1.EnvVar{
			{Name: "PATH", Value: "spec-path"},   // overrides host
			{Name: "EXTRA", Value: "spec-extra"}, // new
		},
		map[string]string{
			"EXTRA":  "secret-extra", // overrides spec
			"SECRET": "secret-only",  // new
		},
	)

	// Convert to map for assertion convenience.
	m := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}

	require.Equal(t, "spec-path", m["PATH"], "spec.Env overrides host allowlist")
	require.Equal(t, "secret-extra", m["EXTRA"], "secret overrides spec.Env")
	require.Equal(t, "secret-only", m["SECRET"], "secret-only key passes through")
}
