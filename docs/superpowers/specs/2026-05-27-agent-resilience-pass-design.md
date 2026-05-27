# Agent Resilience Pass — Design

> **Status:** accepted 2026-05-27
> **Closes:** [znet/nodemanager#4](https://code.znet/znet/nodemanager/issues/4), [znet/nodemanager#9](https://code.znet/znet/nodemanager/issues/9)

## Problem

Two distinct ways the agent can become "alive but useless":

1. **Reconcile loop dies silently.** Process keeps running, controller-runtime metrics may still flow, but Reconcile is never called. Observed 2026-05-18 when three dns-jail agents stopped reconciling within ~90s of each other, coinciding with a DNS rollout that changed the agent's apiserver resolution path. Operator went days before noticing.

2. **Reconcile gets stuck inside a call.** A single Reconcile enters the body and blocks indefinitely on something uncancellable — an NFS-hung syscall, a libucl call that ignores ctx, a goroutine deadlock, or (the motivating case) the OTLP trace exporter retrying for 60s while the trace backend is broken. The watchdog's entry-heartbeat happily got bumped before the wedge; the agent is stuck.

The agent must catch (1) hard-fast and (2) at least surface, because (2) often resolves itself eventually but masks the worst class of bug.

A related class of bug: nodemanager itself becoming a consumer of the broken thing it's trying to fix. The trace exporter is the prime example — when the trace backend is the casualty of a network change, the agent that's supposed to push the fix is also the one being slowed by the same casualty.

## Goals

- Detect "loop dead" (Reconcile never called) → exit so the supervisor restarts cleanly.
- Detect "Reconcile stuck in-flight" → surface as metric + WARN log; do not exit (legitimate slow operations exist).
- Make OTLP trace export bounded and disable-able so the agent is never blocked by its own telemetry.

## Non-goals

- **Detecting "Reconcile is slow but progressing."** No way to tell from outside; not worth the surface.
- **Auto-recovering from a wedge.** Restart-via-supervisor is the recovery; the watchdog only decides when.
- **Tracking span drop counts as a Prometheus metric.** The OTel SDK has internal counters; no public Go API to expose them as a Prometheus shim, and the marginal value over "the metric we already have for backend health" is small.

---

## Part 1 — Watchdog (#4)

### Two independent signals

| Signal | What it catches | Action |
|---|---|---|
| `lastReconcileEntered` timestamp older than `WatchdogStaleThreshold` | Reconcile is never called (reflector silently halted, work queue starved) | exit(74) |
| In-flight reconcile older than `WatchdogSlowThreshold` | A single Reconcile entered the body and hasn't returned | WARN log + metric; **no exit** |

### Heartbeat

A package-level `atomic.Int64` storing Unix-nano. Updated at the top of every Reconcile body in:
- `internal/controller/common/configset_controller.go`
- `internal/controller/common/managednode_controller.go`
- `internal/controller/freebsd/poudriere_controller.go`

One shared signal; we don't care which controller bumped it.

### In-flight tracker

A `sync.Map` keyed by `"<controller-name>/<namespace>/<name>"`, value `time.Time` (entry time). Each Reconcile:

```go
key := "common.configset/" + req.NamespacedName.String()
tracker.Begin(key)
defer tracker.End(key)
// reconcile body
```

The watchdog ticker (1m) walks `tracker.Snapshot()`. For each entry older than `WatchdogSlowThreshold`:
- Emit one WARN log with `key`, `age`, `threshold`.
- Set `nodemanager_reconcile_in_flight_duration_seconds{controller, key}` gauge to the current age.

Entries that complete normally are removed by the `defer tracker.End(key)`. Stale entries that never complete remain in the map until the process exits.

### Watchdog Runnable

A `controller-runtime` `manager.Runnable`, registered via `mgr.Add()`. Manager lifecycle → cleanly cancelled on shutdown.

```go
type Watchdog struct {
    stale           time.Duration
    slow            time.Duration
    interval        time.Duration
    logger          *slog.Logger
    exit            func(int)   // os.Exit in prod
    tracker         *InFlightTracker
    heartbeat       *atomic.Int64
    inFlightMetric  *prometheus.GaugeVec
}

func (w *Watchdog) Start(ctx context.Context) error { /* ticker loop */ }
```

### Config preflight

If `WatchdogStaleThreshold > 0` AND `ConfigSetConfig.ReconcilePeriod == 0`, `Watchdog.Start()` returns an error before the ticker starts. Reason: with event-only reconciles, a deliberately idle fleet has no Reconcile entries, the heartbeat never updates, and the watchdog false-positives into a crash loop.

This is a hard fail. Operators must opt into a sane requeue cadence before opting into the watchdog. WARN-and-continue is rejected — silent false-positive crash-loops are exactly the bug we're fixing.

### Configuration

```
-watchdog.stale-threshold duration   Exit if no Reconcile entry in this duration. 0=disabled. Default: 10m.
-watchdog.slow-threshold  duration   WARN + metric if a single Reconcile has been in-flight longer than this. 0=disabled. Default: 15m.
```

Defaults: 10m stale, 15m slow. Both non-zero (always-on); both individually disable-able.

### New metric

```
nodemanager_reconcile_in_flight_duration_seconds (gauge)
  Labels: controller, key
  Set to the current age of any in-flight Reconcile older than WatchdogSlowThreshold.
  Cleared by the next watchdog tick if the Reconcile completed.
```

Cardinality bound: at most `MaxConcurrentReconciles` × number of controllers concurrently slow. In steady state, zero series. During an incident, a handful.

### Exit code

`74` (EX_IOERR from sysexits.h). Non-zero, supervisor-friendly, distinct from `1` (generic error) so operators can grep for watchdog-driven exits in supervisor logs.

---

## Part 2 — Trace export resilience (#9)

### Bounded exporter

```go
traceExporter, err := otlptracegrpc.New(ctx,
    otlptracegrpc.WithTimeout(3*time.Second),
    otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
        Enabled:         true,
        InitialInterval: 500 * time.Millisecond,
        MaxInterval:     1 * time.Second,
        MaxElapsedTime:  3 * time.Second,
    }),
)
```

Caps per-export at 3s and total retry envelope at 3s. Worst-case worker goroutine occupancy per failing batch: ~6s.

### Bounded batch processor

```go
sdktrace.NewBatchSpanProcessor(traceExporter,
    sdktrace.WithBatchTimeout(2*time.Second),
    sdktrace.WithExportTimeout(5*time.Second),
)
```

`ExportTimeout=5s` is the outer bound the processor enforces on `Export()` calls regardless of the exporter's own retry config. Belt-and-suspenders.

Queue size + max-batch stay at SDK defaults. Drop-on-full is already the default in current Go SDK versions.

### Shutdown

5s → 2s. The daemon should exit promptly when supervisor or `os.Exit(74)` says so; a broken backend must not extend that.

### Disable knob

```
-tracing.enabled bool   Emit OTLP traces. Default: true.
```

When `false`, skip the exporter + processor + provider setup entirely. The global `TracerProvider` stays as the SDK's default noop. All existing `r.tracer.Start(...)` calls compile and run; they just produce zero spans. No goroutines, no connections, no shutdown work needed.

This is the recovery escape hatch: if the trace backend is what nodemanager is trying to fix, an operator can deploy with `-tracing.enabled=false` on the affected hosts, push the fix, then re-enable.

---

## Components

| File | Change |
|---|---|
| `internal/controller/common/config.go` | New `WatchdogConfig` group with `StaleThreshold`, `SlowThreshold`. New `TracingConfig` with `Enabled`. Wire into `ControllerConfig`. |
| `internal/controller/common/watchdog.go` (new) | `Heartbeat()` package function, `InFlightTracker`, `Watchdog` Runnable, `NewWatchdog`. |
| `internal/controller/common/configset_controller.go` | Call `Heartbeat()` and bracket the body with `tracker.Begin/End` at top of Reconcile. |
| `internal/controller/common/managednode_controller.go` | Same. |
| `internal/controller/freebsd/poudriere_controller.go` | Same (cross-package call into `common.Heartbeat` and a tracker exposed via the manager). |
| `internal/controller/common/metrics.go` | New `nodemanager_reconcile_in_flight_duration_seconds` gauge. |
| `cmd/main.go` | (a) Wire `mgr.Add(common.NewWatchdog(...))`; (b) gate exporter setup on `cfg.Tracing.Enabled`; (c) apply bounded options when enabled. |
| `internal/controller/common/watchdog_test.go` (new) | Tests with injectable exit fn + clock. |
| `cmd/main_test.go` | If tractable, integration smoke test of `tracing.enabled=false` path. (May skip if the existing test harness doesn't cover main; out of scope if so.) |

### Where the InFlightTracker lives

Single instance, created in `cmd/main.go`, passed to each controller's `New*Reconciler` constructor (already a pattern there for `cfg`, `system`, `locker`). The freebsd `PoudriereReconciler` gets it too — cross-package call, same pattern as `commonv1` imports already in place.

---

## Data flow

```
                  ┌─ ConfigSet.Reconcile ─┐
ApiServer event ──┼─ ManagedNode.Reconcile─┼─→ tracker.Begin(key) ──→ body ──→ tracker.End(key)
                  └─ Poudriere.Reconcile  ─┘     │                                │
                                                 └─→ Heartbeat()                  └─ (deferred)

                  Watchdog ticker (1m):
                  ├─ if now - heartbeat > stale: exit(74)
                  └─ for each in-flight > slow: WARN + gauge.Set(age)
                                                  ↑
                                                  └─ Prometheus scrape

                  Process exit → manager ctx cancel → Watchdog.Start returns nil
                  Trace shutdown (2s ctx, best-effort) → process exits
```

---

## Testing

### Watchdog unit tests

1. **Startup grace.** `heartbeat == 0` → no exit even after threshold elapses on the tick.
2. **Healthy.** Heartbeat refreshed every tick → no exit.
3. **Stale.** Last heartbeat older than `stale` → exit fn called with `74`. Test injects `func(int) { ch <- code }`.
4. **Disabled.** `stale == 0` → ticker never inspects heartbeat; no exit ever.
5. **Preflight reject.** `stale > 0 && reconcilePeriod == 0` → `Start(ctx)` returns a wrapped error containing both flag names.
6. **In-flight tracker basics.** `Begin(k)` → `Snapshot()` contains `k` with a recent time. `End(k)` → `Snapshot()` empty.
7. **Stuck reconcile.** `Begin(k)` without `End(k)`, advance clock past `slow`, tick the watchdog. Assert: WARN logged, gauge for `k` set to a value ≥ `slow.Seconds()`. Exit fn NOT called.
8. **Recovery.** After a stuck reconcile resolves (`End(k)`), the next watchdog tick removes the gauge series via `DeleteLabelValues` so dashboards don't show stale values.

### Tracing tests

Tracing setup lives in `main.go` which has no existing test harness. We won't grow one for this. Manual verification:
- Start with `-tracing.enabled=false`, send a ConfigSet event, confirm no goroutines target the trace backend (`netstat`, log absence of OTLP dial attempts).
- Start with default (enabled), block the trace endpoint at the firewall, confirm reconciles complete in normal time and `make test` still passes.

---

## Risks

- **Watchdog crash-loop on a real apiserver outage.** If the apiserver is down for >10m, no Reconcile attempts can succeed. Heartbeat-on-*entry* (not on success) still fires though — controller-runtime will retry the apiserver List loops, and even errored Reconciles bump the heartbeat. So this risk is largely moot; the watchdog only fires when Reconcile is *never called*, which requires the reflector itself to be dead.
- **InFlightTracker leak on a permanently-stuck reconcile.** If a goroutine is wedged in a syscall that ignores ctx, the `defer tracker.End` never runs. The map grows by one entry per stuck reconcile. In practice the watchdog detects this within 15m via the slow-signal, an operator restarts the agent, and the map resets. Acceptable.
- **Tracing disabled → debugging blind spot.** Operators must remember to re-enable. Document in the runbook for the watchdog/incident scenario.
- **Bounded retry could drop spans during transient backend slowness.** Yes — by design. Best-effort is the point. If spans must be reliable, log structured fields are still emitted and metrics are unaffected.

## Out of scope

- A circuit-breaker on the trace exporter (auto-disable after N consecutive failures). The bounded retry config makes this unnecessary; the worst case is the worker goroutine spending ~6s every 2s on failed exports, which is annoying but not blocking.
- Periodic self-list against the apiserver as an additional liveness signal. Adds a second control path and load on the apiserver; the heartbeat-via-event-driven-Reconcile is sufficient given the watchdog only fires when *no* reconciles happen for 10m.
- Per-controller `MaxConcurrentReconciles` tuning. Possibly relevant for stuck reconciles (a stuck one blocks a worker, not the whole controller), but separate from this change.
