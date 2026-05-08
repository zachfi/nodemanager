# Monitoring Poudriere Builds

Three nodemanager metrics describe the state of every PoudriereBulk:

- `nodemanager_poudriere_bulk_runs_total{node,bulk,result}` — counter
- `nodemanager_poudriere_bulk_duration_seconds{node,bulk}` — histogram
- `nodemanager_poudriere_last_bulk_timestamp_seconds{node,bulk}` — gauge

This page is the operator's cookbook for turning them into the views
you actually want at 03:00 on a Saturday.

## What each `result` label means

The `result` dimension on `nodemanager_poudriere_bulk_runs_total` has
four values; understanding the difference matters because alerts and
rate calculations on this metric mean different things.

| `result` | Meaning | Emitted by |
|---|---|---|
| `success` | A run completed and produced packages. | InProcess at end of `poudriere bulk`. Command at end of a successful Status poll. |
| `error` | A run failed. | InProcess on `poudriere bulk` non-zero exit. Command on dispatch program failure or terminal `state=failed` from Status. |
| `skipped` | The reconcile detected matching `Status.Hash` and last result was `Succeeded`, so the heavy work was skipped. | Both executors. |
| `dispatched` | A Command-mode reconcile invoked the Dispatch program successfully but the build is now in flight (terminal result will arrive later). | Command only. |

Critically: `success + error + skipped + dispatched` is **not** the
total reconcile count — `skipped` is its own thing. A bulk in steady
state with `reconcilePeriod=6h` and no input changes will increment
`skipped` once every 6 hours and never touch `success` or `error`.

## Useful queries

### Build success rate over the last day

```promql
sum by (bulk) (increase(nodemanager_poudriere_bulk_runs_total{result="success"}[1d]))
/
sum by (bulk) (increase(nodemanager_poudriere_bulk_runs_total{result=~"success|error"}[1d]))
```

Excludes `skipped` and `dispatched` from the denominator — they're not
attempts at building. A bulk that only ever skips will produce
`NaN`/no data, which is the right answer (no signal).

### Time since last completion, by bulk

```promql
time() - nodemanager_poudriere_last_bulk_timestamp_seconds
```

Pair with a Grafana stat panel and a threshold at your longest
`reconcilePeriod`. Anything older than that is suspicious and the
`PoudriereBuildStale` alert will fire (default threshold: 24h; tune
in `monitoring/alerts/nodemanager.libsonnet`).

### Skip rate (am I rebuilding too aggressively?)

```promql
sum by (bulk) (rate(nodemanager_poudriere_bulk_runs_total{result="skipped"}[1h]))
/
sum by (bulk) (rate(nodemanager_poudriere_bulk_runs_total[1h]))
```

A high skip rate is normal and good — it means the input-hash gate is
saving you actual builds. A bulk that's never skipping while its
inputs aren't changing is a bug (likely a non-deterministic spec field
leaking into the hash; file an issue).

### p95 and p99 build duration, by bulk

```promql
histogram_quantile(0.95,
  sum by (bulk, le) (rate(nodemanager_poudriere_bulk_duration_seconds_bucket[1h]))
)

histogram_quantile(0.99,
  sum by (bulk, le) (rate(nodemanager_poudriere_bulk_duration_seconds_bucket[1h]))
)
```

The histogram buckets are `[10, 30, 60, 300, 900, 1800, 3600, 7200, 14400]`
seconds — tuned so a fast no-op rerun (~10s) and a full rebuild
(several hours) both fall into well-separated buckets. The
`PoudriereBuildLong` alert fires when p95 exceeds 4 hours.

### Dispatched-but-not-yet-finished count (Command mode)

```promql
sum by (bulk) (
  increase(nodemanager_poudriere_bulk_runs_total{result="dispatched"}[15m])
  -
  increase(nodemanager_poudriere_bulk_runs_total{result=~"success|error"}[15m])
)
```

A persistently positive value means the Status program isn't reporting
terminal states — likely the upstream system has lost the run, or the
Status bridge has a bug translating an upstream state we haven't
mapped. Check `kubectl get poudrierebulk -o json | jq '.items[].status'`
for the in-flight `lastDispatchedRunID` values.

### Failure breakdown by node

```promql
sum by (node) (rate(nodemanager_poudriere_bulk_runs_total{result="error"}[6h]))
```

If one host shows a spike, the per-host rate-limiter (`limits.LongRunning`,
1 token / 30s) caps how fast errors can accumulate; sustained failures
typically mean a bridge auth issue or a build-host infrastructure
problem rather than a per-bulk content issue.

## Alert reference

All in `monitoring/alerts/nodemanager.libsonnet`:

| Alert | Severity | Triggers |
|---|---|---|
| `NodeManagerPoudriereBuildFailed` | warning | Any `result=error` increase in the last 15m |
| `NodeManagerPoudriereBuildStale` | warning | `last_bulk_timestamp_seconds` > 24h ago, holds for 15m |
| `NodeManagerPoudriereBuildLong` | warning | p95 duration > 4h over 1h window, holds for 1h |

The thresholds are sized for a typical home/lab deployment with a
few bulks at 6-hour reconcile periods. For larger setups (every-hour
rebuilds, dozens of bulks), tighten `Stale`'s 24h threshold; for
larger ports lists where 4h builds are normal, loosen `Long`'s
threshold or add a `severity=info`-only variant.

## Grafana dashboard sketch

A useful single-pane-of-glass for poudriere has:

1. **Top row**: stat panels for each metric in aggregate
   - Total bulks reconciling (count of distinct `bulk` series)
   - Builds in flight (the dispatched-but-unfinished query above)
   - Builds in last 24h, by result (stacked bar)
2. **Status table**: one row per bulk, columns:
   - Last result (color-coded from
     `nodemanager_poudriere_bulk_runs_total{result=~"success|error"}`
     using `topk by (bulk) (1, ...)`)
   - Time since last completion
   - p95 duration over 1d
   - Skip rate over 1d
3. **Time series**:
   - Run rate by result, stacked
   - Duration p50/p95/p99
   - Last-completion-age

A starter Grafana JSON for this isn't shipped in the repo yet — the
queries above are the building blocks. If you build a useful one,
PR it to `monitoring/dashboards/`.

## Related

- [Metrics reference](metrics.md) — full list of nodemanager metrics
- [Command Executor Contract](../poudriere/command-contract.md) — what
  `result=dispatched` actually means in the state machine
- [Bridge Security](../security/poudriere-bridges.md) — what to check
  when a Command-mode bulk's failures look like auth issues
