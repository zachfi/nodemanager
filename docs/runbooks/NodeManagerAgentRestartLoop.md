# NodeManagerAgentRestartLoop

**Severity:** warning

## Meaning

The nodemanager process on a host has restarted more than 3 times in 30 minutes.
The `instance` label identifies the host. This commonly means the agent
watchdog is firing (process exits with code 74 after logging
`watchdog: stale; exiting`).

## Impact

The agent is not staying up long enough to reconcile reliably. The node drifts
from desired state, and the restart churn can mask the underlying fault.

## Diagnosis

Check why the supervisor keeps relaunching it:

```sh
# systemd: journalctl -u nodemanager -n 100
# FreeBSD: tail the rc.d service log / daemon supervisor output
```

Look specifically for the watchdog message and the exit code:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=30m | grep -i -E 'watchdog|exiting'
```

If the watchdog is firing, the agent is alive but Reconcile stopped running —
see `NodeManagerReconcileStuck` and capture a goroutine dump.

## Remediation

- **Reconcile not running**: investigate controller-runtime reflector health,
  apiserver connectivity, and the agent's kubeconfig — a dead informer stops
  reconciles while the process lives, which is exactly what the watchdog guards
  against.
- **Crash on startup**: if it dies before becoming ready, read the first lines of
  each boot for a config/permission error.
- **Flapping dependency**: confirm the apiserver and any required local sockets
  are stable.
