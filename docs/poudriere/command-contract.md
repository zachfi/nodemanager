# PoudriereBulk Command Executor Contract

When `PoudriereBulk.spec.executor.type` is `Command`, nodemanager
delegates the build to an external program — a shell script, a Go
binary, or anything `os/exec` can run. This page is the authoritative
specification of the contract between nodemanager and that program.

The Go side of the contract lives in
[`pkg/cmdrunner/types.go`](https://github.com/zachfi/nodemanager/blob/main/pkg/cmdrunner/types.go)
— if a doc here ever drifts from the code, the code wins.

**Contract version: `freebsd.nodemanager/v1`**

## Why a contract instead of a typed integration

nodemanager could embed a Forgejo client. Or a Woodpecker client. Or
both. The contract avoids that — it lets a small bridge program speak
whichever upstream system you want, and makes nodemanager generic. If
Forgejo's API changes its dispatch shape next year, you fix one shell
script. If you swap to Woodpecker tomorrow, you write a different
script. The CR shape doesn't change.

The contract is intentionally light: stdin/stdout JSON, exit codes for
success/failure. No gRPC, no plugin protocol, no daemon to keep alive.
Everything an executor needs to know fits on this page.

## The two operations

A bridge implements two programs:

- **Dispatch** — invoked when nodemanager wants to start a build.
  Required.
- **Status** — invoked to learn the state of a previously dispatched
  run. Optional; when absent, dispatches are fire-and-forget.

Both programs are configured per-bulk under
`spec.executor.command.{dispatch,status}`. The slice is `argv` —
the first element is the executable, the rest are arguments.

## Dispatch

| Channel | Content |
|---|---|
| `argv` | `spec.executor.command.dispatch` (executable + arguments) |
| `env` | Allowlisted host env (`PATH`, `HOME`, `USER`, `LANG`, `LC_ALL`, `TZ`) plus `spec.executor.command.env` (literal) and `spec.executor.command.secretEnv` (resolved at every reconcile) |
| `stdin` | One JSON object — `BulkDispatchInput` (schema below). Bounded ≤ 64 KB. |
| `stdout` | The trimmed last non-empty line, interpreted as an opaque `runID` string. Bounded ≤ 64 KB; excess truncated. |
| `stderr` | Captured up to 64 KB. On non-zero exit surfaces verbatim into `Status.LastError` (truncated to 4 KB). |
| exit code | `0` = success. Anything else = failure. |
| timeout | `spec.executor.command.dispatchTimeout` (default 60s). Subprocess receives `SIGKILL` if it exceeds. |

### `BulkDispatchInput` (stdin)

```json
{
  "apiVersion": "freebsd.nodemanager/v1",
  "kind": "BulkDispatchInput",
  "metadata": {
    "name": "personal-amd64",
    "namespace": "nodemanager",
    "uid": "9f27aabb-4f8c-4d4c-8e3a-12345abcdef0",
    "generation": 4
  },
  "spec": {
    "jail":  "14amd64",
    "tree":  "personal",
    "ports": ["net/curl", "shells/zsh"]
  }
}
```

**Field semantics:**

- `metadata.name`, `metadata.namespace` — identity. Use them in your
  bridge's logs so an operator can correlate.
- `metadata.uid` — stable per-CR identifier, useful for tagging
  external-system resources.
- `metadata.generation` — bumps on every spec change. Useful as
  audit context: "this run was for generation 4 of the spec."
- `spec.jail`, `spec.tree`, `spec.ports[]` — the build inputs.
  Pass these through to your build system however it expects them.

Operational fields (`reconcilePeriod`, `executor`) are deliberately
**not** in the dispatch input. They configure how nodemanager behaves;
the executor doesn't need them.

### Stdout — the run ID

Whatever single non-empty trailing line your dispatch program prints is
treated as an opaque run identifier. nodemanager stores it in
`status.lastDispatchedRunID` and passes it back to your Status
program if/when it polls.

A "run identifier" can be anything: a Forgejo run number, a UUID, a
job-queue key, a database row ID — nodemanager doesn't parse it.
Empty stdout is valid and means "I don't have a meaningful run ID";
nodemanager will still record the dispatch but skip status polling
even if a Status program is configured.

### Exit codes

- `0` — dispatch succeeded. nodemanager records the run as in-flight
  (or, with no Status program, as `Succeeded` immediately) and
  proceeds to the next reconcile.
- non-zero — dispatch failed. `status.lastBuildResult` becomes
  `Failed`, `status.lastError` is set to a string of the form
  `dispatch program exit N: <stderr truncated to 4 KB>`, and the
  bulk's `Degraded` condition flips to true.

## Status

Optional. Without it, dispatched runs are fire-and-forget: nodemanager
records the dispatch and never checks back. With it, nodemanager
periodically asks the program "is this run done yet?" until the
program reports a terminal state.

| Channel | Content |
|---|---|
| `argv` | `spec.executor.command.status` |
| `env` | Same as Dispatch |
| `stdin` | One line — the `runID` returned by Dispatch |
| `stdout` | One JSON object — `BulkRunStatus` (schema below). Bounded ≤ 64 KB. |
| `stderr` | Captured; on non-zero exit logged at warn level, status reconcile retried. |
| exit code | `0` = parsed and applied. Non-zero = transient failure, retry. |
| timeout | `spec.executor.command.statusTimeout` (default 30s) |

### `BulkRunStatus` (stdout)

```json
{
  "apiVersion": "freebsd.nodemanager/v1",
  "kind": "BulkRunStatus",
  "state": "running",
  "url": "https://code.znet/zachfi/build-infra/actions/runs/427",
  "error": ""
}
```

| Field | Required | Notes |
|---|---|---|
| `apiVersion` | yes | `freebsd.nodemanager/v1`. Mismatch is a parse error. |
| `kind` | yes | `BulkRunStatus`. Mismatch is a parse error. |
| `state` | yes | One of `running`, `success`, `failed`, `unknown`. |
| `url` | no | Human-readable URL where an operator can view the run. Surfaced into `status.lastDispatchedRunURL` and shown by `kubectl describe`. |
| `error` | no | Human-readable error message. Set when `state=failed`. Surfaced into `status.lastError`. |

### State machine

```
running   → controller requeues at statusPollInterval (30s) and polls again
success   → terminal: status.lastBuildResult=Succeeded, in-flight cleared
failed    → terminal: status.lastBuildResult=Failed, error → status.lastError
unknown   → treated like running (poll again)
```

`unknown` is for cases where the executor genuinely cannot determine
the state — for instance, the upstream system has lost the run.
nodemanager keeps polling rather than declaring failure, so a
recovered upstream eventually surfaces a terminal state.

## Versioning

The contract is identified on every JSON document by `apiVersion` and
`kind`. Today: `freebsd.nodemanager/v1` paired with
`BulkDispatchInput` or `BulkRunStatus`.

**Forward-compatibility rule:** executor programs MUST ignore unknown
fields. nodemanager will only ever add fields within `v1`; never
rename or remove. If an incompatible change is needed, a new
`apiVersion` will be introduced and bridges will choose which to
parse. JSON parsers in every common language ignore unknown fields by
default, so this is usually free.

**Backward-compatibility promise:** every field in v1 stays. A bridge
written today against v1 will keep working until the day a v2 is
explicitly opted into.

## Output limits

Stdout and stderr are each capped at **64 KB** per invocation. Bytes
beyond that are dropped, and `RunResult.Truncated` is set internally
(visible in nodemanager's logs). The cap exists to prevent a flooding
executor from OOMing the controller. For dispatch, 64 KB is plenty —
the contract requires only a single run-ID line. For status, the
JSON document fits well under that. For diagnostic stderr on failure,
the controller truncates further to 4 KB before storing in
`status.lastError` so the CR doesn't grow without bound.

## Reference bridges

Working examples live in
[`config/samples/scripts/`](https://github.com/zachfi/nodemanager/tree/main/config/samples/scripts):

- `poudriere-dispatch-forgejo.sh` — Dispatch via Forgejo Actions
  `workflow_dispatch`. Handles Forgejo's quirk of not returning the
  run ID from `/dispatches` by polling `/runs` after a short delay.
- `poudriere-status-forgejo.sh` — Status via Forgejo's run API.
  Translates `status × conclusion` into the contract's state.

A complete `PoudriereBulk` using the Forgejo bridges:

```yaml
apiVersion: freebsd.nodemanager/v1
kind: PoudriereBulk
metadata:
  name: personal-amd64
  namespace: nodemanager
spec:
  jail: 14amd64
  tree: personal
  ports: ["net/curl", "shells/zsh"]
  reconcilePeriod: 6h
  executor:
    type: Command
    command:
      dispatch: ["/usr/local/libexec/poudriere-dispatch-forgejo.sh"]
      status:   ["/usr/local/libexec/poudriere-status-forgejo.sh"]
      env:
        - name: FORGEJO_BASE_URL
          value: https://code.znet
        - name: FORGEJO_REPO
          value: zachfi/build-infra
        - name: FORGEJO_WORKFLOW
          value: poudriere-build.yml
      secretEnv:
        - name: FORGEJO_TOKEN
          secretRef:
            name: forgejo-poudriere-pat
            key: token
      dispatchTimeout: 30s
      statusTimeout:   15s
```

## Security

Executor programs run with the same privileges as nodemanager. Secret
values pass through env vars; nodemanager guarantees its own logs
never include them, but the script author is responsible for not
echoing them or enabling `set -x` near a secret.

See [Bridge Security](../security/poudriere-bridges.md) for the
threat model and required script-author hygiene.
