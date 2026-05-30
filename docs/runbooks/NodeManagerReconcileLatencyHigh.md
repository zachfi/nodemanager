# NodeManagerReconcileLatencyHigh

**Severity:** warning

## Meaning

The p99 reconcile latency for a nodemanager controller (`managednode` or
`configset`) has exceeded 30s for 10 minutes. The `controller` and `instance`
labels identify the affected reconciler and node.

## Impact

Slow reconciles mean desired state converges late. ConfigSet changes, upgrades,
and file/service updates are delayed, and a backed-up workqueue can starve other
objects waiting on the same controller.

## Diagnosis

Identify what each reconcile is spending time on:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=15m | grep -i reconcile
```

Common culprits are slow on-node operations: a package-manager database lock,
a hung template `validate` command, or a service that takes a long time to
restart. Check the controller's own latency histogram:

```promql
histogram_quantile(0.99,
  rate(controller_runtime_reconcile_time_seconds_bucket{job=~"nodemanager.*"}[5m])
)
```

## Remediation

- **On-node slowness**: SSH to `{{ instance }}` and check for a locked package
  database (`pacman`/`apk`/`pkg`), full disk, or a wedged systemd/rc unit.
- **Validator hangs**: a ConfigSet exec/file validator with no timeout can block
  the reconcile — see `NodeManagerReconcileStuck` if latency climbs without bound.
- **Apiserver pressure**: confirm the controller is not being throttled
  (client-side rate limiting in the logs) and that the apiserver is healthy.
