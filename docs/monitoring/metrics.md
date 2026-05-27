# Metrics

nodemanager exposes Prometheus metrics on `:8080/metrics` (configurable via
`--metrics-bind-address`).

## Controller metrics

These are emitted by [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
and are available for all controllers (`managednode`, `configset`).

| Metric | Type | Description |
|---|---|---|
| `controller_runtime_reconcile_total` | Counter | Reconcile attempts by controller and result (`success`, `error`, `requeue`). |
| `controller_runtime_reconcile_errors_total` | Counter | Total reconcile errors by controller. |
| `controller_runtime_reconcile_time_seconds` | Histogram | Reconcile duration by controller. |
| `controller_runtime_active_workers` | Gauge | Active reconcile goroutines. |

## Application metrics

Custom metrics emitted by nodemanager itself.

### ConfigSet

| Metric | Labels | Description |
|---|---|---|
| `nodemanager_configset_apply_total` | `node`, `configset`, `result` | ConfigSet apply attempts (`success` / `error`). |
| `nodemanager_configset_apply_duration_seconds` | `node`, `configset` | How long each ConfigSet apply takes. |
| `nodemanager_last_configset_apply_timestamp_seconds` | `node`, `configset` | Unix timestamp of the last successful apply. Used for staleness alerts. |
| `nodemanager_file_changes_total` | `node`, `configset`, `result` | Files changed during a ConfigSet apply. |

### Packages

| Metric | Labels | Description |
|---|---|---|
| `nodemanager_package_operations_total` | `node`, `operation`, `result` | Package manager operations. `operation` is `install`, `remove`, or `upgrade`. |

### Services

| Metric | Labels | Description |
|---|---|---|
| `nodemanager_service_operations_total` | `node`, `service`, `operation`, `result` | Service manager operations. `operation` is `start`, `stop`, `restart`, `enable`, or `disable`. `result` is `success`, `error`, or `exited` (start/restart only — daemon exited within `StartVerifyDelay` of the call). |

### Jails (FreeBSD)

| Metric | Labels | Description |
|---|---|---|
| `nodemanager_jail_operations_total` | `node`, `jail`, `operation`, `result` | Jail lifecycle operations. `operation` is `provision`, `start`, or `delete`. |
| `nodemanager_jail_provision_duration_seconds` | `node`, `jail` | Duration of `EnsureJail` (release download, ZFS clone, conf/fstab write). |

### Poudriere builds (FreeBSD)

| Metric | Labels | Description |
|---|---|---|
| `nodemanager_poudriere_bulk_runs_total` | `node`, `bulk`, `result` | Total poudriere bulk runs by result. `result` ∈ `success`, `error`, `skipped`, `dispatched`. `dispatched` only appears for Command-mode bulks (a dispatch was sent; the terminal result lands later via the status program). |
| `nodemanager_poudriere_bulk_duration_seconds` | `node`, `bulk` | Histogram of poudriere bulk run durations. Buckets cover 10s (a no-op rerun) through 4h (full rebuilds). |
| `nodemanager_poudriere_last_bulk_timestamp_seconds` | `node`, `bulk` | Unix timestamp of the most recent terminal completion (success or failure). Used for the `PoudriereBuildStale` alert. |

See [Poudriere monitoring](poudriere.md) for example PromQL queries
that combine these metrics into the views an operator typically wants.

### Upgrades

| Metric | Labels | Description |
|---|---|---|
| `nodemanager_upgrade_total` | `node`, `result` | Node upgrade attempts (`success` / `error`). |
| `nodemanager_upgrade_duration_seconds` | `node` | Duration of node upgrade operations. |
| `nodemanager_last_upgrade_timestamp_seconds` | `node` | Unix timestamp of the last successful upgrade. Used for staleness alerts. |

## Alerts

Alert rules are defined in the [monitoring mixin](https://github.com/zachfi/nodemanager/tree/main/monitoring)
and loaded into Mimir. See the [runbooks](../runbooks/NodeManagerReconcileErrors.md) for
remediation guidance.
