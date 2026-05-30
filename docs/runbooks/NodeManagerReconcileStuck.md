# NodeManagerReconcileStuck

**Severity:** warning

## Meaning

A reconcile has been in-flight on a node for more than 5 minutes without
completing. The `controller`, `key`, and `node` labels identify the wedged
reconcile. This is surfaced by the agent watchdog's
`nodemanager_reconcile_in_flight_duration_seconds` gauge.

## Impact

The agent is blocked on a single object and is not making progress on anything
else. If it stays stuck the watchdog will eventually exit the process (see
`NodeManagerAgentRestartLoop`).

## Diagnosis

**Before restarting**, capture a goroutine dump so the hang can be diagnosed —
the stack will show exactly where it is blocked:

```sh
curl -s 'http://<node>:<metrics-port>/debug/pprof/goroutine?debug=2' > goroutine.txt
```

Then look at what the stuck key was doing:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=15m | grep <key>
```

## Remediation

- **Uncancellable syscall**: a package manager or service command with no
  timeout can block forever — identify it from the goroutine dump and add/await a
  timeout or fix the on-node condition (lock, hung mount, dead mirror).
- **Upstream hang**: an apiserver or external dependency that never responds;
  confirm connectivity.
- **Last resort**: restart the agent only after capturing the goroutine dump,
  otherwise the root cause is lost.
