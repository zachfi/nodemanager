# NodeManagerPoudriereBuildFailed

**Severity:** warning

## Meaning

A Poudriere bulk run completed with `result="error"`. The `bulk` and `node`
labels identify the build. Both executor types report this: the InProcess
executor records "error" when `poudriere bulk` exits non-zero; the Command
executor records "error" when the dispatch program fails or the status program
reports `state=failed`.

## Impact

Built packages were not produced (or are incomplete) for that bulk, so consumers
of those packages cannot install the new versions.

## Diagnosis

Read the failure detail from the bulk's status:

```sh
kubectl describe poudrierebulk -n nodemanager <bulk>
```

The `LastError` field carries the message; for Command-mode bulks the
`LastDispatchedRunURL` field links directly to the upstream build log. Also
check the controller logs:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=30m | grep -i poudriere
```

## Remediation

- **Port build failure**: open the linked build log and fix the failing port
  (dependency break, fetch failure, patch rejection).
- **Executor/bridge failure**: for Command mode, verify the dispatch program and
  the Forgejo/Woodpecker runner are healthy and reachable.
- **Jail/ports tree**: confirm the referenced PoudriereJail and PoudrierePorts
  are provisioned and current.
