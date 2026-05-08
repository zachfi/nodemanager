# Bridge Security: PoudriereBulk Command Executors

The `Command` executor runs an external program with secrets in its
environment. This page is the operational threat model: what
nodemanager guarantees, what the script author has to guarantee, and
where the boundary between the two is.

## Threat model

Three failure modes worth being explicit about:

| # | Threat | Counter |
|---|---|---|
| 1 | Secret value leaks into nodemanager's logs or OTEL traces | nodemanager handles |
| 2 | Secret value leaks via process listing (`ps eww`, `/proc/<pid>/environ`, `procstat -e`) | OS handles (with caveats) |
| 3 | Secret value leaks via the bridge script itself (`set -x`, `echo $TOKEN`, error messages including auth headers) | script author handles |

Each mitigation is described below.

## What nodemanager guarantees

These are properties of `pkg/cmdrunner` enforced by the implementation
and locked in by tests in
[`pkg/cmdrunner/runner_test.go`](https://github.com/zachfi/nodemanager/blob/main/pkg/cmdrunner/runner_test.go).
A regression that breaks any of these is a security regression and
will fail CI.

### Secrets resolved at every reconcile, never cached

`spec.executor.command.secretEnv[*].secretRef` is read from the
Kubernetes API on every reconcile. There's no in-memory cache of
secret values. If you rotate a Secret in the cluster, the next
reconcile (within 30 seconds for active polling, or sooner on any
event) picks up the new value automatically.

### Resolved values never enter the log scope

The runner's structured log lines reference secret **sources** by
name only — for example `forgejo-poudriere-pat#token→FORGEJO_TOKEN`.
The values themselves are confined to the `cmd.Env` slice of the
`os/exec.Cmd` and become unreachable when `Run()` returns.

The regression test
`TestRun_SecretValuesNeverAppearInLogs` stages a sentinel value in a
fake Secret, runs the runner with a script that consumes the secret,
then asserts the captured slog buffer contains the source descriptor
**but not the sentinel value**. Adding any new `slog`/`fmt` call in
runner.go that touches a resolved value will fail this test.

### Strict env allowlist

Subprocesses inherit only `PATH`, `HOME`, `USER`, `LANG`, `LC_ALL`,
`TZ` from the host environment. Everything else comes from
`spec.executor.command.env` (literal) or `spec.executor.command.secretEnv`
(resolved).

This prevents accidental leakage of operator-side debug env to
executors — `KUBECONFIG`, `AWS_*`, `GCS_*`, ssh agent sockets, etc. If
a bridge needs additional host env it must be opted in explicitly via
the CR.

### Output bounds

Stdout and stderr are each capped at 64 KB per invocation. A flooding
executor cannot OOM the controller. Stderr from a failed dispatch is
further truncated to 4 KB before being written to `status.lastError`
so failed builds cannot inflate the CR.

### Process kill on timeout

`spec.executor.command.dispatchTimeout` (default 60s) and
`statusTimeout` (default 30s) bound how long a single invocation can
run. Exceeding the timeout sends `SIGKILL` to the subprocess via
`exec.CommandContext`; the runner returns an error and the
reconciliation retries on the next event.

### Secrets only as env, never as argv

The cmdrunner API has no facility for putting resolved secret values
into `argv`. They go in `cmd.Env` and stay there. This blocks the
common mistake of constructing a command line like
`["myscript", "--token", "$VALUE"]` which would expose the value via
`ps`.

## What the OS provides

These are platform properties; nodemanager doesn't control them, but
they shape your operational risk.

### `/proc/<pid>/environ` — Linux

Mode `0600`, owner = process uid. nodemanager runs as root, so:

- Other unprivileged users cannot read it.
- Other root processes can. If you're concerned about a co-resident
  malicious root process, env-vars are not the right channel.
- A coredump of nodemanager would include the env. Coredumps from
  the controller are typically disabled in production; double-check
  your systemd service unit if you change defaults.

### `/proc` — FreeBSD

Not mounted by default. `procstat -e <pid>` requires either ownership
of the process or `kern.procctl` access (root). Same threat surface as
Linux: root processes can read; unprivileged users cannot.

### Process list (`ps`, `top`)

Standard `ps` does not show env vars. `ps eww` does, but is
restricted by the OS to processes you own. nodemanager runs as root,
so `ps eww` from another root would expose env. From an unprivileged
account it does not.

## What the script author has to guarantee

These are entirely outside nodemanager's control. If a bridge breaks
any of these rules, secrets can leak even though nodemanager's own
guarantees are intact.

### Don't echo secrets

```sh
# BAD — token appears in stdout
echo "Authorization: token $FORGEJO_TOKEN"

# GOOD — curl handles the header internally
curl -H "Authorization: token $FORGEJO_TOKEN" ...
```

`curl` does not include the `Authorization` header in its progress
output, error messages, or the `-v` debug output, so this is safe.
Other HTTP clients may differ; check your tool's behaviour.

### Don't `set -x` once secrets are in scope

`set -x` echoes every command before execution, including expanded
variables. If you need shell tracing for debugging, put it before
the `:` line that asserts secrets exist, or clear secrets first:

```sh
set -x   # safe — no secret bound yet
: "${FORGEJO_BASE_URL:?}"
set +x   # disable BEFORE the secret check
: "${FORGEJO_TOKEN:?}"
```

The reference bridges deliberately do not `set -x` anywhere.

### Don't include secrets in error messages

Error output goes to stderr and surfaces into `status.lastError`,
which is visible to anyone with `get poudrierebulk` access. Errors
including a `curl -v` dump or a printed env are a leak.

```sh
# BAD — env dump on failure exposes everything
curl ... || { env >&2; exit 1; }

# GOOD — short message, no values
curl ... || { echo "forgejo dispatch failed" >&2; exit 1; }
```

### Validate inputs before using them in URLs

The bridge gets `runID` on stdin from a previous dispatch. If your
status program shoves it directly into a URL path without validation,
a malicious dispatch script could inject traversal sequences. The
reference status bridge validates that the run ID is a positive
integer before using it:

```sh
case "$RUN_ID" in
    ''|*[!0-9]*) echo "invalid run ID: $RUN_ID" >&2; exit 1 ;;
esac
```

This is "defence in depth" — the dispatch program is yours, so in
practice you control what it returns. The validation guards against a
future change to the bridge that accidentally introduces
operator-controlled content into the run ID.

## Secret rotation

To rotate a Forgejo PAT (or any other credential):

1. Generate a new secret value in the upstream system.
2. Update the Kubernetes Secret with the new value:
   ```sh
   kubectl create secret generic forgejo-poudriere-pat \
     --from-literal=token=NEW_TOKEN \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
3. The next reconcile picks up the new value automatically. There's no
   nodemanager restart needed and no cached value to invalidate.
4. Once you've confirmed the new value works, revoke the old one in
   the upstream system.

## Secret hygiene checklist for new bridges

When writing a new bridge script, before you ship:

- [ ] No `set -x` after secret env vars are referenced
- [ ] No `echo $TOKEN`, no `printenv`, no `env` redirected to stderr
- [ ] HTTP client used does not log auth headers
   (`curl` and `wget` are fine; some Python `requests` debug modes do)
- [ ] Error messages on failure don't dump env or response bodies
   from authenticated calls
- [ ] Inputs from stdin are validated before being used in URLs,
   commands, or shell-expansions
- [ ] The script runs with `set -eu` so a typo in a variable name
   doesn't silently bind to empty string
- [ ] Timeouts on outbound HTTP (`curl --max-time`) are set if the
   upstream system is known to occasionally hang

## Reporting a leak

If you discover a path where a secret value reaches a log, an OTEL
span, a status field, or any other observable surface from a
nodemanager-controlled code path, please file an issue tagged
`security`. Reproducing tests are very welcome.

The
[`TestRun_SecretValuesNeverAppearInLogs`](https://github.com/zachfi/nodemanager/blob/main/pkg/cmdrunner/runner_test.go)
test is the reference shape for such a regression: stage a sentinel
value, exercise the path, assert the sentinel doesn't appear where it
shouldn't.
