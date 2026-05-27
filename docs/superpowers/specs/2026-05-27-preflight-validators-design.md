# Preflight Validators on File / Exec Specs — Design

> **Status:** accepted 2026-05-27
> **Closes:** [znet/nodemanager#3](https://code.znet/znet/nodemanager/issues/3)

## Problem

Today, when nodemanager renders a templated file the result lands on disk before any consumer sees it. If the rendered content is malformed:

- **nsd** reads a broken zone, `*.znet` resolution dies cluster-wide until the zone is fixed and reloaded.
- **bgpd** segfaults or refuses to start on a syntax error in `bgpd.conf`, dropping a router from the network.
- **pf** rejects the ruleset on first reload after the next reconcile, dropping firewall rules.

Two existing proposals work around this by hand-rolling validator wrappers in `Exec` chains:

- [Proposal 0040](https://code.znet/znet/deployment_tools/src/branch/main/docs/proposals/0040-nodemanager-preflight-config-validation.md) — `bgpd -nf` and `pfctl -nf` before restart.
- [Proposal 0113](https://code.znet/znet/deployment_tools/src/branch/main/docs/proposals/0113-dns-zone-publication-resilience.md) — `nsd-checkzone` + previous-file restore in shell.

Both want the same primitive: **render → validate → commit-or-rollback**. The shell wrappers race (file lands on disk before validator runs), are repetitive, and produce inconsistent error reporting.

## Goals

- Add a single CRD field (`Validate`) shared by `File` and `Exec` types that runs an external command before committing the change.
- Make the file-write path atomic: if validation fails, the previous on-disk content survives untouched, and no subscriber is notified.
- Surface failures as a metric value (`result="validate_failed"`), a structured WARN log with stderr tail, and a `ManagedNode.status.configsets[].conditions` entry.
- Provide two failure modes: `rollback` (skip this item, continue the ConfigSet) and `abort` (halt the ConfigSet).

## Non-goals

- **Cross-file consistency.** A validator that needs to see all three new files together — out of scope. Single-file is the 80% case.
- **Recovering a service that already restarted on a bad config.** Covered by `StartVerifyDelay` (issue #2, already shipped).
- **Templating the validator command via gomplate.** A fixed sprintf-style substitution set (`${STAGED}`, `${TARGET}`) is sufficient and avoids template-engine surprises in shell args.
- **A general "validate any Exec body" gate that runs an Exec's own command in dry-run mode.** Validators are separate commands; the Exec body remains exactly what the operator wrote.

---

## The CRD addition

`api/common/v1/configset_types.go`:

```go
// Validate gates the application of a File or Exec on the exit code of an
// external command. On non-zero exit, OnFailure decides whether to skip this
// item (rollback) or halt the whole ConfigSet (abort).
//
// For File validators, ${STAGED} substitutes to the temp file path containing
// the rendered content; ${TARGET} substitutes to the final destination path.
// For Exec validators, no substitution applies — the command is what runs,
// independent of any file in the ConfigSet.
type Validate struct {
	// Command is the executable (absolute path or PATH-resolved name).
	Command string `json:"command,omitempty"`
	// Args are positional arguments. ${STAGED} and ${TARGET} are substituted
	// in each element (File validators only).
	Args []string `json:"args,omitempty"`
	// TimeoutSeconds caps the validator runtime. 0 means default (30s).
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// OnFailure selects the failure semantics. "rollback" (default) skips this
	// item and continues the ConfigSet; "abort" halts the ConfigSet apply.
	OnFailure string `json:"onFailure,omitempty"`
}
```

New fields on the existing types:

```go
type File struct {
    // ... existing fields ...
    Validate *Validate `json:"validate,omitempty"`
}

type Exec struct {
    // ... existing fields ...
    Validate *Validate `json:"validate,omitempty"`
}
```

Pointer type so `omitempty` produces a clean YAML round-trip. `nil` means "no validation, behave as before" — backwards compatible.

A small helper applies defaults:

```go
const (
    validateDefaultTimeout    = 30 * time.Second
    validateOnFailureRollback = "rollback"
    validateOnFailureAbort    = "abort"
)

func (v *Validate) Timeout() time.Duration {
    if v.TimeoutSeconds <= 0 {
        return validateDefaultTimeout
    }
    return time.Duration(v.TimeoutSeconds) * time.Second
}

func (v *Validate) Abort() bool {
    return v.OnFailure == validateOnFailureAbort
}
```

---

## File validation flow

The current `writeFileContent` does **content → chown → chmod** directly on the final target path. The handler functions' built-in change-detection saves work when the file already matches spec. The new staged flow preserves both properties (atomicity AND skip-when-unchanged) by checking equivalence BEFORE staging:

```
1. Render content (template + secrets + configmaps as today).
2. Skip-when-unchanged short-circuit:
     desiredSHA  := sha256(rendered content)
     currentSHA  := sha256 of file.Path's current bytes (if it exists; 0 otherwise)
     currentOwner, currentMode := stat(file.Path)
     if currentSHA == desiredSHA && currentOwner matches spec && currentMode matches spec:
        return changed=false, "", nil   // nothing to do; validator NOT run
3. tempPath := os.CreateTemp(dir(file.Path), base(file.Path)+".nm-staged-")
   // os.CreateTemp creates with mode 0600 owned by the agent (root).
4. handler.WriteContentFile(tempPath, content)
   // tempPath is still mode 0600 — restrictive while the validator runs.
5. If file.Validate != nil:
     ctx2, cancel := context.WithTimeout(ctx, file.Validate.Timeout())
     command, args := substitute(file.Validate.Command, file.Validate.Args, tempPath, file.Path)
     output, exit, err := r.system.Exec().RunCommand(ctx2, command, args...)
     cancel()
     if exit != 0 || err != nil:
         os.Remove(tempPath)
         r.logger.Warn("validator failed",
            "path", file.Path,
            "command", command,
            "exit", exit,
            "stderr", lastKB(output),   // RunCommand returns stderr on failure
         )
         fileChangesTotal.WithLabelValues(nodeName, configSetName, file.Path, "validate_failed").Inc()
         setStatusCondition(...)
         return changed=false, backupHash="", validationErr{Abort: file.Validate.Abort()}
6. handler.Chown(tempPath, owner, group)    // apply operator spec — only after validator pass
7. handler.SetMode(tempPath, mode)
8. os.Rename(tempPath, file.Path)            // atomic on POSIX, same fs
9. return changed=true, backupHash, nil
```

**Security properties:**
- The temp file is born mode 0600 (root). It stays 0600 throughout content write and validator run. The operator-specified owner/mode are only applied between steps 6 and 8 — a microsecond-scale window adjacent to the rename.
- The temp file lives in the same directory as `file.Path`, NOT `/tmp`. This makes rename atomic (same filesystem) AND avoids the cross-directory permissions concern entirely. If `file.Path` is `/usr/local/etc/nsd/znet.zone`, the temp is `/usr/local/etc/nsd/znet.zone.nm-staged-XXXXXX`.
- On any failure between steps 3 and 8, the temp file is removed; the existing target is never touched.

**Change-detection note:** because the temp file is brand new each time, calling `handler.Chown` and `handler.SetMode` will always report `changed=true` (they're comparing against fresh state). That return value becomes informational only; the real "skip when unchanged" check happens in step 2 at the start. Operators don't notice a behavior change — the metric increments and log lines remain attached to whether the FINAL target's content/perms differed from spec.

**Validator user:** the validator runs as the agent (root). No `User` field on `Validate` — if an operator needs the validator to run as a specific user, they sudo within the command (e.g. `sudo -u nsd nsd-checkzone znet ${STAGED}`). This keeps the temp permissions concern simple: root can always read its own 0600 file.

**Invariant:** the existing on-disk file is untouched until step 8. Render error, mkstemp failure, content-write failure, validator failure, validator timeout, chown failure, setmode failure — all leave the original intact. This is actually stronger than today's behavior (today a write failure can leave a half-written file).

`handleFileSet` checks the returned error: if it's a `validationErr` with `Abort=false`, it logs but does NOT add the path to `changedFiles` and continues to the next file. If `Abort=true`, the error propagates up so the ConfigSet halts.

### Substitution — what `${STAGED}` and `${TARGET}` mean

The validator command needs a way to point at the file it should validate. That file is at the temp path during validation — the final target still holds the OLD content. The tokens expose this distinction:

- `${STAGED}` → the temp file path with the freshly rendered content (e.g. `/usr/local/etc/nsd/znet.zone.nm-staged-Abc123`).
- `${TARGET}` → the final destination path (e.g. `/usr/local/etc/nsd/znet.zone`). Included for the uncommon case where a validator needs to read the previous file to check the new one against it.

Example: validating an nsd zone before commit.

```yaml
- path: /usr/local/etc/nsd/znet.zone
  template: nsd-zone.tmpl
  validate:
    command: nsd-checkzone
    args:
      - znet
      - ${STAGED}
```

At runtime nodemanager renders the template into the temp path, substitutes `${STAGED}`, and executes:

```
nsd-checkzone znet /usr/local/etc/nsd/znet.zone.nm-staged-Abc123
```

On exit 0 → rename temp → `znet.zone`. On non-zero → remove temp, never touch `znet.zone`.

Without `${STAGED}`, an operator can't write a meaningful validator — they'd have nothing to point at. The token is the entire interface between the validator and the staged file.

Implementation:

```go
func substitute(cmd string, args []string, staged, target string) (string, []string) {
    rep := strings.NewReplacer("${STAGED}", staged, "${TARGET}", target)
    out := make([]string, len(args))
    for i, a := range args {
        out[i] = rep.Replace(a)
    }
    return rep.Replace(cmd), out
}
```

Applied to both `Command` and each `Args` element. Exec validators don't get substitution (there's no staged file); `${STAGED}` / `${TARGET}` in an Exec's `Validate.Args` would pass through literally — operators shouldn't use them there.

---

## Exec validation flow

No staging — `Validate` for Exec is a pure gate:

```
For each exe in serviceSet:
    if exe.Validate != nil:
        ctx2, cancel := WithTimeout(ctx, exe.Validate.Timeout())
        output, exit, err := r.system.Exec().RunCommand(ctx2, exe.Validate.Command, exe.Validate.Args...)
        cancel()
        if exit != 0 || err != nil:
            WARN log + metric (see below).
            if exe.Validate.Abort(): return err
            continue   // skip body, continue ConfigSet
    Run exe body (existing logic).
```

No `${STAGED}` / `${TARGET}` substitution — there's no staged file for Exec.

---

## Metric inventory

### Existing: `nodemanager_file_changes_total`

Today: `result ∈ {success, error}` (labels: `node, configset, path`).
After: `result ∈ {success, error, validate_failed}`.

No label changes; `validate_failed` is an additional enum value. Documented in `docs/monitoring/metrics.md`.

### New: `nodemanager_exec_operations_total`

The acceptance criterion specifies an "equivalent exec metric" with `result="validate_failed"`. No exec counter exists today, so this PR introduces one alongside the validator work:

```go
execOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
    Name: "nodemanager_exec_operations_total",
    Help: "Total number of ConfigSet Exec runs.",
}, []string{"node", "command", "result"})
```

Labels:
- `node` — matches every other `nodemanager_*` metric.
- `command` — `filepath.Base(exe.Command)`. Bounded cardinality (~30 distinct executables across the fleet) and operator-readable.
- `result ∈ {success, error, validate_failed, skipped}`.
  - `skipped` covers "subscribe_files set but no triggering file changed this reconcile."
  - `validate_failed` is set instead of running the body when the validator gate trips.

This is a minor scope-add over the strict #3 issue but is needed to satisfy the acceptance criterion. Cleaner now than adding it ad-hoc later.

---

## Status condition

`ManagedNode.status.configsets[].conditions` already supports `metav1.Condition` entries. Validator failures surface as:

```yaml
- type: Ready
  status: "False"
  reason: ValidationFailed
  message: "validation failed for 2 file(s): /etc/nsd/znet.zone, /etc/bgpd.conf"
  lastTransitionTime: <reconcile time>
```

Cleared by the next reconcile if no validator fires. If both a `Conflicted` condition (existing) and `ValidationFailed` are present, both appear — they have distinct `reason` values.

---

## Components

| File | Change |
|---|---|
| `api/common/v1/configset_types.go` | Add `Validate` struct, helper methods (`Timeout`, `Abort`), `Validate *Validate` field on `File` and `Exec`. |
| `api/common/v1/zz_generated.deepcopy.go` | Regenerated by `make generate`. |
| `config/crd/bases/common.nodemanager_configsets.yaml` | Regenerated by `make manifests`. |
| `internal/controller/common/metrics.go` | New `execOperationsTotal` counter; register in `init`. Docs comment on the new `validate_failed` value for `fileChangesTotal`. |
| `internal/controller/common/configset_controller.go` | (a) `writeFileContent` reshaped around staged temp + validator. (b) New `runValidator` helper. (c) `handleExecutions` gains validator gate + `execOperationsTotal` increments. (d) New `validationErr` sentinel error type to propagate abort/rollback decisions out of `writeFileContent`. (e) `handleFileSet` aggregates validator failures into a `ValidationFailed` status condition. |
| `internal/controller/common/configset_controller_test.go` | Five new specs (see Tests below). |
| `internal/controller/common/mock_test.go` | Extend `mockExecHandler` with a per-command response map so tests can return non-zero exit codes for specific validator invocations. |
| `docs/monitoring/metrics.md` | Update `nodemanager_file_changes_total` row to document `validate_failed`. New row for `nodemanager_exec_operations_total`. |

---

## Tests

1. **File validator passes** — content lands on disk, `changedFiles` contains path, no `validate_failed` increment, no status condition.
2. **File validator fails, rollback (default)** — original target unchanged (read it back via the mock's stored content), path NOT in `changedFiles` (subscriber service is NOT restarted by `SubscribeFiles`), `fileChangesTotal{result="validate_failed"}` ticks, WARN logged, status condition `Ready=False reason=ValidationFailed` set.
3. **File validator fails, abort** — same as #2 plus `handleFileSet` returns the wrapped error → assertable from the test.
4. **File validator timeout** — validator that exits the test's clock past `TimeoutSeconds` is killed via ctx cancellation, treated as failure with the same rollback semantics.
5. **Exec validator fails, rollback** — body's `RunCommand` was NOT called, `execOperationsTotal{result="validate_failed"}` ticks, next exec in the slice still runs.

Mock changes: `mockExecHandler` needs a way to return specific exit codes per command. Add a `responses map[string]execResponse` field where the key is the executable basename; default response is `(stdout="", exit=0, err=nil)`.

---

## Risks

- **Validator binary missing on the host.** `exec.CommandContext` returns a "executable not found" error. Same path as a validator returning non-zero — handled as a validation failure with whatever OnFailure was set. Operators are expected to install validators alongside the daemons they validate; this isn't nodemanager's problem to solve.
- **Validator hangs.** Per-call ctx timeout (default 30s) kills it. Process group cleanup is handled by `exec.CommandContext` — child processes inherit ctx cancellation only if the validator launched them with the same ctx, but in practice validators are short-lived single binaries.
- **Mode bits or owner can't be set on the temp file.** Rare but possible (e.g. owner doesn't exist on this host). Treated as a write failure — temp file removed, original untouched, error propagated. Same shape as a validator failure with `abort`.
- **Renames across filesystems.** The staged temp file is created in the SAME directory as `Path` specifically to avoid this. Documented in code comments.
- **The validator itself depends on the file existing.** E.g. `bgpd -nf /etc/bgpd.conf` checks the original location, not the staged temp. For this case the operator writes `bgpd -nf ${STAGED}` to point at the temp. The substitution token is the entire interface.

---

## Acceptance criteria (from issue #3)

- [x] New `Validate` field on `File` and `Exec` CRD types.
- [x] Validator runs in temp-file stage; on failure, original target untouched, file path NOT propagated to `changedFiles`.
- [x] New `result="validate_failed"` value on `nodemanager_file_changes_total` and equivalent exec metric.
- [x] `ManagedNode.status` reflects validator failures.
- [x] Test case: configset writes a file whose validator returns non-zero; assert the on-disk file is unchanged and the subscriber service is NOT restarted.
