# Agent Resilience Pass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the nodemanager agent detect "loop dead" and "reconcile stuck" failure modes, and make OTLP trace export best-effort so the agent is never blocked by its own telemetry.

**Architecture:** A single `Watchdog` struct in `internal/controller/common/` owns an entry-heartbeat (atomic Unix-nano timestamp) and an in-flight reconcile tracker (sync.Map). Reconcilers call `w.Track(controller, name)` at the top of `Reconcile`, which bumps the heartbeat, records the in-flight entry, and returns a cleanup func for `defer`. A `manager.Runnable` ticks every minute: exits the process if the heartbeat is stale; sets a gauge for in-flight reconciles older than the slow threshold. OTLP exporter setup gains bounded timeouts and a `-tracing.enabled` kill switch.

**Tech Stack:** Go 1.24, controller-runtime v0.22, prometheus/client_golang, sync.Map + sync/atomic, opentelemetry-go SDK + OTLP/gRPC exporter, Ginkgo v2 + Gomega, jsonnet for the mixin.

**Spec:** [`docs/superpowers/specs/2026-05-27-agent-resilience-pass-design.md`](../specs/2026-05-27-agent-resilience-pass-design.md)
**Closes:** [znet/nodemanager#4](https://code.znet/znet/nodemanager/issues/4), [znet/nodemanager#9](https://code.znet/znet/nodemanager/issues/9)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `internal/controller/common/config.go` | Modify | Add `WatchdogConfig` + `TracingConfig` types, wire into `ControllerConfig`, register flags. |
| `internal/controller/common/watchdog.go` (new) | Create | `Watchdog` struct: heartbeat, in-flight tracker, `Track` API, `Start` Runnable, preflight check. |
| `internal/controller/common/watchdog_test.go` (new) | Create | Ginkgo specs covering Track, ticker behavior, preflight, exit-on-stale, slow-signal. |
| `internal/controller/common/metrics.go` | Modify | New `nodemanager_reconcile_in_flight_duration_seconds` gauge. |
| `internal/controller/common/configset_controller.go` | Modify | Add `watchdog *Watchdog` field; bracket Reconcile body with `Track`. |
| `internal/controller/common/managednode_controller.go` | Modify | Same. |
| `internal/controller/freebsd/poudriere_controller.go` | Modify | Same (cross-package — imports `common`). |
| `internal/controller/freebsd/jail_controller.go` | Modify | Same. |
| `cmd/main.go` | Modify | Build `Watchdog`, pass to reconciler constructors, `mgr.Add(watchdog)`. Bound OTLP exporter; gate on `cfg.Tracing.Enabled`. |
| `monitoring/alerts/nodemanager.libsonnet` | Modify | Add `NodeManagerReconcileStuck` + `NodeManagerAgentRestartLoop`. |
| `monitoring/dashboards/nodemanager-agent-health.libsonnet` (new) | Create | Four-panel agent-health dashboard. |
| `docs/monitoring/metrics.md` | Modify | Add row for the new gauge. |

---

## API summary (locked down for cross-task consistency)

```go
// internal/controller/common/watchdog.go

// Watchdog owns the heartbeat + in-flight tracker + ticker. The zero value
// is NOT safe; use NewWatchdog. A nil *Watchdog is safe to call Track on
// (returns a no-op cleanup) — reconcilers in unit tests may pass nil.
type Watchdog struct {
    cfg              WatchdogConfig
    nodeName         string
    reconcilePeriod  time.Duration  // for preflight check
    logger           *slog.Logger
    exit             func(int)      // os.Exit in prod; injected in tests
    now              func() time.Time
    interval         time.Duration  // ticker cadence; 1m in prod
    heartbeat        atomic.Int64   // unix nano
    inFlight         sync.Map       // map[string]time.Time
}

func NewWatchdog(cfg WatchdogConfig, nodeName string, reconcilePeriod time.Duration, logger *slog.Logger) *Watchdog

// Track is the reconciler hook. Returns a cleanup func to defer.
// Safe to call on a nil receiver.
func (w *Watchdog) Track(controller, name string) func()

// Start implements manager.Runnable. Returns an error from preflight if
// cfg.StaleThreshold > 0 && reconcilePeriod == 0.
func (w *Watchdog) Start(ctx context.Context) error
```

Track key format: `controller + "/" + name`. The "controller" label values used across the codebase:

| Reconciler | controller arg |
|---|---|
| ConfigSetReconciler | `"controller.common.configset"` |
| ManagedNodeReconciler | `"controller.common.managednode"` |
| PoudriereReconciler | `"controller.freebsd.poudriere"` |
| JailReconciler | `"controller.freebsd.jail"` |

These match the existing `otel.Tracer(...)` names already used in each Reconcile, so a trace and an in-flight key share a vocabulary.

Exit code on stale heartbeat: **74** (EX_IOERR).

---

## Task 1: WatchdogConfig + TracingConfig in `config.go`

**Files:**
- Modify: `internal/controller/common/config.go`

Add two config groups before existing `ManagedNodeConfig`. Both register flags via the standard `RegisterFlagsAndApplyDefaults` pattern already used by `FileBucketConfig`.

- [ ] **Step 1.1: Add `WatchdogConfig` type and registration**

In `internal/controller/common/config.go`, add this block right after the existing `FileBucketConfig` `RegisterFlagsAndApplyDefaults` closing brace (around line 31):

```go
// WatchdogConfig controls the agent self-watchdog: catches "Reconcile loop
// stopped" by exiting the process, and "Reconcile stuck in-flight" by
// surfacing a metric + WARN log. Both signals are independently disable-able.
type WatchdogConfig struct {
	// StaleThreshold: exit if no Reconcile entry in this duration. 0 disables.
	StaleThreshold time.Duration `json:"staleThreshold,omitempty"`
	// SlowThreshold: WARN + metric if a single Reconcile has been in-flight
	// longer than this. 0 disables.
	SlowThreshold time.Duration `json:"slowThreshold,omitempty"`
}

func (c *WatchdogConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&c.StaleThreshold, prefix+".stale-threshold", 10*time.Minute, "Exit the agent (code 74) if no Reconcile entry occurs in this duration. 0 disables. Requires configset.reconcile-period > 0 when enabled.")
	f.DurationVar(&c.SlowThreshold, prefix+".slow-threshold", 15*time.Minute, "Surface a WARN log and metric series when a single Reconcile has been in-flight longer than this. 0 disables.")
}
```

- [ ] **Step 1.2: Add `TracingConfig` type and registration**

Immediately after the WatchdogConfig registration:

```go
// TracingConfig controls OTLP trace export. Setting Enabled=false skips all
// exporter and processor setup so the agent never blocks on a broken trace
// backend — recovery escape hatch when nodemanager itself is the fix.
type TracingConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

func (c *TracingConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.BoolVar(&c.Enabled, prefix+".enabled", true, "Emit OTLP traces. Set false when the trace backend is the thing nodemanager is trying to fix.")
}
```

- [ ] **Step 1.3: Add fields to `ControllerConfig` and wire registrations**

Update `ControllerConfig` to embed both. Find the struct definition (around line 33) and add two fields after `Notification`:

```go
type ControllerConfig struct {
	MetricsAddr          string
	EnableLeaderElection bool
	ProbeAddr            string
	SecureMetrics        bool
	EnableHTTP2          bool
	Namespace            string
	GomplatePath         string

	ManagedNode  ManagedNodeConfig
	ConfigSet    ConfigSetConfig
	Locker       locker.Config
	FreeBSD      freebsd.ControllerConfig
	Notification notification.Config
	Watchdog     WatchdogConfig
	Tracing      TracingConfig
}
```

Then in `RegisterFlagsAndApplyDefaults` (around line 51), add two registration lines at the end (before the closing `}`):

```go
	c.Watchdog.RegisterFlagsAndApplyDefaults("watchdog", f)
	c.Tracing.RegisterFlagsAndApplyDefaults("tracing", f)
```

- [ ] **Step 1.4: Build**

Run: `make build`
Expected: clean — two binaries produced.

- [ ] **Step 1.5: Commit**

```bash
git add internal/controller/common/config.go
git commit -m "$(cat <<'EOF'
feat(config): add WatchdogConfig and TracingConfig

Prep for the agent resilience pass. Watchdog defaults to 10m stale /
15m slow; tracing defaults to enabled. Both groups follow the existing
RegisterFlagsAndApplyDefaults pattern.

Refs znet/nodemanager#4, znet/nodemanager#9.
EOF
)"
```

---

## Task 2: Watchdog struct + Track API (TDD)

**Files:**
- Create: `internal/controller/common/watchdog.go`
- Create: `internal/controller/common/watchdog_test.go`

Build the Watchdog type bottom-up: heartbeat + in-flight tracker, with a nil-safe `Track` API. Ticker behavior comes in Task 3.

- [ ] **Step 2.1: Write the failing test — nil-safe Track**

Create `internal/controller/common/watchdog_test.go` with:

```go
/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package common

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWatchdog(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Watchdog Suite")
}

var _ = Describe("Watchdog", func() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	Context("Track API", func() {
		It("is a no-op on a nil receiver", func() {
			var w *Watchdog
			done := w.Track("ctrl", "ns/name")
			Expect(done).NotTo(BeNil())
			done() // must not panic
		})

		It("records an in-flight entry and clears it on done()", func() {
			w := NewWatchdog(WatchdogConfig{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
			Expect(w.inFlightCount()).To(Equal(0))

			done := w.Track("ctrl", "ns/name")
			Expect(w.inFlightCount()).To(Equal(1))

			done()
			Expect(w.inFlightCount()).To(Equal(0))
		})

		It("bumps the heartbeat on entry", func() {
			w := NewWatchdog(WatchdogConfig{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
			Expect(w.lastHeartbeatNanos()).To(Equal(int64(0)))

			done := w.Track("ctrl", "ns/name")
			defer done()
			Expect(w.lastHeartbeatNanos()).To(BeNumerically(">", 0))
		})

		It("is safe for concurrent Track calls", func() {
			w := NewWatchdog(WatchdogConfig{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					done := w.Track("ctrl", "key/"+string(rune(i)))
					done()
				}(i)
			}
			wg.Wait()
			Expect(w.inFlightCount()).To(Equal(0))
		})
	})
})

// Helpers used only by tests but kept on the type for clarity.
// Implemented alongside Watchdog in watchdog.go.
var _ = context.Background // silence unused import in initial test scaffold
```

- [ ] **Step 2.2: Run the test to confirm it fails to compile**

Run:

```bash
cd /home/zach/go/src/github.com/zachfi/nodemanager && go test ./internal/controller/common/ -run TestWatchdog 2>&1 | tail -10
```

Expected: `undefined: Watchdog` / `undefined: NewWatchdog` — type doesn't exist yet.

- [ ] **Step 2.3: Implement the Watchdog type**

Create `internal/controller/common/watchdog.go`:

```go
/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package common

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Watchdog owns the heartbeat and in-flight reconcile tracker. Reconcilers
// call Track at the top of Reconcile; the returned cleanup func is deferred
// to clear the in-flight entry on exit.
//
// A nil *Watchdog is safe to call Track on — returns a no-op cleanup —
// so reconcilers in unit tests may leave the field unset.
type Watchdog struct {
	cfg             WatchdogConfig
	nodeName        string
	reconcilePeriod time.Duration
	logger          *slog.Logger

	// Injectable for tests.
	exit     func(int)
	now      func() time.Time
	interval time.Duration

	heartbeat atomic.Int64 // unix nanos; 0 means never bumped (startup grace)
	inFlight  sync.Map     // key: "<controller>/<name>" → value: time.Time
}

// NewWatchdog builds a Watchdog with production defaults (exit=nil sentinel,
// now=time.Now, interval=1m). The exit func is set by the Start method's
// caller via WithExit; default Start uses os.Exit.
func NewWatchdog(cfg WatchdogConfig, nodeName string, reconcilePeriod time.Duration, logger *slog.Logger) *Watchdog {
	return &Watchdog{
		cfg:             cfg,
		nodeName:        nodeName,
		reconcilePeriod: reconcilePeriod,
		logger:          logger.With("subsystem", "watchdog"),
		now:             time.Now,
		interval:        time.Minute,
	}
}

// Track records a Reconcile entry. Bumps the heartbeat, registers the
// in-flight entry, and returns a cleanup func to defer.
//
// Safe to call on a nil receiver.
func (w *Watchdog) Track(controller, name string) func() {
	if w == nil {
		return func() {}
	}
	w.heartbeat.Store(w.now().UnixNano())
	key := controller + "/" + name
	w.inFlight.Store(key, w.now())
	return func() {
		w.inFlight.Delete(key)
	}
}

// Test-only accessors. Lowercased to keep them package-private.

func (w *Watchdog) inFlightCount() int {
	n := 0
	w.inFlight.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

func (w *Watchdog) lastHeartbeatNanos() int64 {
	return w.heartbeat.Load()
}
```

- [ ] **Step 2.4: Run the tests to confirm they pass**

Run:

```bash
cd /home/zach/go/src/github.com/zachfi/nodemanager && go test ./internal/controller/common/ -run TestWatchdog -v 2>&1 | tail -20
```

Expected: 4 passing specs.

- [ ] **Step 2.5: Commit**

```bash
git add internal/controller/common/watchdog.go \
        internal/controller/common/watchdog_test.go
git commit -m "$(cat <<'EOF'
feat(common): Watchdog struct with nil-safe Track API

Heartbeat (atomic.Int64) + in-flight tracker (sync.Map). Reconcilers
will call Track at the top of Reconcile; the returned cleanup func
clears the in-flight entry on exit. Nil-safe so unit tests can leave
the field unset.

Ticker behavior, exit-on-stale, and the slow-signal metric come in
follow-up commits.

Refs znet/nodemanager#4.
EOF
)"
```

---

## Task 3: In-flight metric

**Files:**
- Modify: `internal/controller/common/metrics.go`

Add the gauge that the watchdog ticker will populate. Defined before the ticker so the metric name is stable.

- [ ] **Step 3.1: Add the gauge**

In `internal/controller/common/metrics.go`, add this block immediately after the existing `serviceOperationsTotal` definition (around line 42):

```go
	// reconcileInFlightDuration exposes the age of any Reconcile call that
	// has been running longer than WatchdogSlowThreshold. Empty in steady
	// state; a small handful of series during incidents. Watchdog ticker
	// resets the gauge each tick and re-populates from current in-flight
	// scan, so series for resolved reconciles disappear.
	reconcileInFlightDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nodemanager_reconcile_in_flight_duration_seconds",
		Help: "Age of an in-flight Reconcile call exceeding the watchdog slow threshold.",
	}, []string{"node", "controller", "key"})
```

Then add it to the `metrics.Registry.MustRegister(...)` block at the bottom of the file. Find the closing of the existing list and add `reconcileInFlightDuration,` before the closing parenthesis:

```go
	metrics.Registry.MustRegister(
		buildInfoGauge,
		configSetApplyTotal,
		configSetApplyDuration,
		packageOperationsTotal,
		serviceOperationsTotal,
		fileChangesTotal,
		upgradeTotal,
		upgradeDuration,
		lastUpgradeTimestamp,
		lastConfigSetApplyTimestamp,
		configSetConflictsTotal,
		configSetAppliedResourceVersion,
		reconcileInFlightDuration,
	)
```

- [ ] **Step 3.2: Build**

Run: `make build`
Expected: clean.

- [ ] **Step 3.3: Commit**

```bash
git add internal/controller/common/metrics.go
git commit -m "$(cat <<'EOF'
feat(metrics): add nodemanager_reconcile_in_flight_duration_seconds

Gauge surfaced by the watchdog ticker for any Reconcile running longer
than WatchdogSlowThreshold. Empty in steady state; bounded handful of
series during incidents.

Refs znet/nodemanager#4.
EOF
)"
```

---

## Task 4: Watchdog ticker — exit on stale, gauge on slow (TDD)

**Files:**
- Modify: `internal/controller/common/watchdog.go`
- Modify: `internal/controller/common/watchdog_test.go`

The Runnable implementation. Heartbeat-stale → exit; in-flight-slow → set gauge + WARN; preflight refuses to start with bad config combos.

- [ ] **Step 4.1: Write failing tests for Start behavior**

Append to `internal/controller/common/watchdog_test.go`, after the existing `Context("Track API", ...)` block but inside the outer `Describe`:

```go
	Context("Start (Runnable)", func() {
		makeWatchdog := func(cfg WatchdogConfig, reconcilePeriod time.Duration, exit func(int)) *Watchdog {
			w := NewWatchdog(cfg, "test-node", reconcilePeriod, logger)
			w.exit = exit
			w.interval = 10 * time.Millisecond
			return w
		}

		It("preflight rejects stale>0 with reconcilePeriod==0", func() {
			w := makeWatchdog(WatchdogConfig{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, 0, func(int) {})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := w.Start(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("watchdog.stale-threshold"))
			Expect(err.Error()).To(ContainSubstring("configset.reconcile-period"))
		})

		It("does not exit during startup grace (heartbeat==0)", func() {
			exitCode := make(chan int, 1)
			w := makeWatchdog(
				WatchdogConfig{StaleThreshold: 50 * time.Millisecond, SlowThreshold: time.Minute},
				time.Minute,
				func(c int) { exitCode <- c },
			)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			Expect(w.Start(ctx)).To(Succeed())
			Expect(exitCode).NotTo(Receive(), "watchdog must not exit before any reconcile has happened")
		})

		It("exits with code 74 when heartbeat is older than stale threshold", func() {
			exitCode := make(chan int, 1)
			w := makeWatchdog(
				WatchdogConfig{StaleThreshold: 30 * time.Millisecond, SlowThreshold: time.Minute},
				time.Minute,
				func(c int) { exitCode <- c },
			)
			// Set heartbeat to a moment far enough in the past to be stale on the next tick.
			w.heartbeat.Store(time.Now().Add(-time.Hour).UnixNano())

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			Expect(w.Start(ctx)).To(Succeed())
			Eventually(exitCode, "300ms", "10ms").Should(Receive(Equal(74)))
		})

		It("sets the in-flight gauge for a stuck reconcile and clears it on completion", func() {
			w := makeWatchdog(
				WatchdogConfig{StaleThreshold: 0, SlowThreshold: 20 * time.Millisecond},
				time.Minute,
				func(int) {},
			)
			w.heartbeat.Store(time.Now().UnixNano()) // healthy heartbeat

			// Simulate a stuck reconcile: Track but never call the cleanup.
			done := w.Track("ctrl", "ns/badd")
			DeferCleanup(done)

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			startCh := make(chan error, 1)
			go func() { startCh <- w.Start(ctx) }()

			Eventually(func() float64 {
				return counterValue(reconcileInFlightDuration.MetricVec, "test-node", "ctrl", "ns/badd")
			}, "150ms", "10ms").Should(BeNumerically(">", 0))

			// Resolve the stuck reconcile; gauge should be reset on the next tick.
			done()
			Eventually(func() float64 {
				return counterValue(reconcileInFlightDuration.MetricVec, "test-node", "ctrl", "ns/badd")
			}, "150ms", "10ms").Should(Equal(0.0))

			cancel()
			Expect(<-startCh).To(Succeed())
		})

		It("disabled stale (0) never triggers an exit even with an old heartbeat", func() {
			exitCode := make(chan int, 1)
			w := makeWatchdog(
				WatchdogConfig{StaleThreshold: 0, SlowThreshold: time.Minute},
				time.Minute,
				func(c int) { exitCode <- c },
			)
			w.heartbeat.Store(time.Now().Add(-time.Hour).UnixNano())
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			Expect(w.Start(ctx)).To(Succeed())
			Expect(exitCode).NotTo(Receive())
		})
	})
```

Note: `counterValue` already exists in `mock_test.go` and works on `*prometheus.CounterVec`. The gauge has a different type — we need a sibling helper.

- [ ] **Step 4.2: Add gauge-reading helper to `mock_test.go`**

In `internal/controller/common/mock_test.go`, immediately after the `counterValue` definition, add:

```go
// gaugeValue reads the float64 value of a GaugeVec series. Returns 0 when
// the series does not exist or cannot be encoded.
func gaugeValue(v *prometheus.GaugeVec, labels ...string) float64 {
	m, err := v.GetMetricWithLabelValues(labels...)
	if err != nil {
		return 0
	}
	var pb dto.Metric
	if err := m.Write(&pb); err != nil {
		return 0
	}
	return pb.GetGauge().GetValue()
}
```

Update the test to call `gaugeValue(reconcileInFlightDuration, ...)` instead of `counterValue(reconcileInFlightDuration.MetricVec, ...)`:

```go
			Eventually(func() float64 {
				return gaugeValue(reconcileInFlightDuration, "test-node", "ctrl", "ns/badd")
			}, "150ms", "10ms").Should(BeNumerically(">", 0))
```

(Same substitution for the second `Eventually` block in that spec — change both occurrences.)

- [ ] **Step 4.3: Run the failing tests**

Run:

```bash
cd /home/zach/go/src/github.com/zachfi/nodemanager && go test ./internal/controller/common/ -run TestWatchdog -v 2>&1 | tail -25
```

Expected: at least one FAIL — `Start` method doesn't exist yet.

- [ ] **Step 4.4: Implement Start, preflight, and ticker behavior**

Append to `internal/controller/common/watchdog.go`:

```go
import (
	"context"
	"errors"
	"fmt"
	"os"
)
```

(Update the existing import block to include `context`, `errors`, `fmt`, `os` — alphabetically.)

Add these methods at the end of the file:

```go
// Start implements controller-runtime's manager.Runnable. Returns nil when
// the ctx is cancelled (normal shutdown), or an error if the preflight
// check fails.
func (w *Watchdog) Start(ctx context.Context) error {
	if w.cfg.StaleThreshold > 0 && w.reconcilePeriod == 0 {
		return fmt.Errorf("watchdog.stale-threshold > 0 requires configset.reconcile-period > 0; otherwise an idle fleet false-positives")
	}

	if w.cfg.StaleThreshold == 0 && w.cfg.SlowThreshold == 0 {
		w.logger.Info("both thresholds 0; watchdog disabled")
		<-ctx.Done()
		return nil
	}

	exitFn := w.exit
	if exitFn == nil {
		exitFn = os.Exit
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("watchdog started",
		"stale_threshold", w.cfg.StaleThreshold.String(),
		"slow_threshold", w.cfg.SlowThreshold.String(),
		"interval", w.interval.String(),
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.tick(exitFn)
		}
	}
}

// tick runs the per-interval check. Exposed only via Start; the exit func
// is threaded through so tests can capture it without touching os.Exit.
func (w *Watchdog) tick(exitFn func(int)) {
	now := w.now()

	// Signal 1: heartbeat staleness.
	if w.cfg.StaleThreshold > 0 {
		hb := w.heartbeat.Load()
		if hb != 0 {
			since := now.Sub(time.Unix(0, hb))
			if since > w.cfg.StaleThreshold {
				w.logger.Error("watchdog: no Reconcile entry in threshold; exiting for supervisor restart",
					"since", since.String(),
					"threshold", w.cfg.StaleThreshold.String(),
				)
				exitFn(74)
				return
			}
		}
	}

	// Signal 2: in-flight slow scan. Reset the gauge each tick and rebuild
	// from current in-flight entries — anything that resolved between ticks
	// disappears from the gauge cleanly.
	reconcileInFlightDuration.Reset()
	if w.cfg.SlowThreshold == 0 {
		return
	}
	w.inFlight.Range(func(k, v any) bool {
		key := k.(string)
		startedAt := v.(time.Time)
		age := now.Sub(startedAt)
		if age <= w.cfg.SlowThreshold {
			return true
		}
		controller, name := splitTrackKey(key)
		w.logger.Warn("reconcile in-flight longer than expected",
			"controller", controller,
			"key", name,
			"age", age.String(),
			"threshold", w.cfg.SlowThreshold.String(),
		)
		reconcileInFlightDuration.WithLabelValues(w.nodeName, controller, name).Set(age.Seconds())
		return true
	})

	_ = errors.New // silence unused import if errors package isn't otherwise referenced; safe to remove if other code in this file uses errors.
}

// splitTrackKey reverses the format used in Track: "controller/key".
func splitTrackKey(s string) (controller, name string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
```

Remove the `_ = errors.New` line if `errors` is not otherwise needed. The import list at the top of the file should match exactly what's used: `context`, `fmt`, `log/slog`, `os`, `sync`, `sync/atomic`, `time` (drop `errors` if not used).

- [ ] **Step 4.5: Run the tests to confirm pass**

Run:

```bash
cd /home/zach/go/src/github.com/zachfi/nodemanager && go test ./internal/controller/common/ -run TestWatchdog -v 2>&1 | tail -25
```

Expected: all specs pass (originally 4 + 5 new = 9 Track-and-Start specs).

- [ ] **Step 4.6: Full suite**

Run: `make test 2>&1 | grep -E "^FAIL|^ok " | head -25`
Expected: every package `ok`.

- [ ] **Step 4.7: Commit**

```bash
git add internal/controller/common/watchdog.go \
        internal/controller/common/watchdog_test.go \
        internal/controller/common/mock_test.go
git commit -m "$(cat <<'EOF'
feat(watchdog): ticker, exit-on-stale, slow-signal gauge, preflight

Watchdog.Start implements manager.Runnable. Each tick (1m in prod):
- if heartbeat older than stale: exit(74).
- reset the in-flight gauge and repopulate from current in-flight scan
  for entries older than slow; emit WARN with controller, key, age.

Preflight: refuses to start when stale > 0 but reconcilePeriod == 0
because an event-only fleet false-positives into a crash loop.

Tests use injected exit fn + tight interval; cover startup grace,
stale-exit, slow-gauge, disabled paths, and preflight rejection.

Refs znet/nodemanager#4.
EOF
)"
```

---

## Task 5: Wire Watchdog into ConfigSetReconciler

**Files:**
- Modify: `internal/controller/common/configset_controller.go`

- [ ] **Step 5.1: Add `watchdog` field and constructor parameter**

In `internal/controller/common/configset_controller.go`, find the `ConfigSetReconciler` struct (around line 66). Add a `watchdog` field at the end:

```go
type ConfigSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	tracer trace.Tracer
	logger *slog.Logger
	system handler.System
	locker locker.Locker
	cfg    ConfigSetConfig

	watchdog *Watchdog

	lastResourceVersionMu sync.Mutex
	lastResourceVersion   map[string]string
}
```

Update `NewConfigSetReconciler` (around line 83) to accept the watchdog as a final parameter. Replace:

```go
func NewConfigSetReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg ConfigSetConfig, system handler.System, locker locker.Locker) *ConfigSetReconciler {
	return &ConfigSetReconciler{
		Client:              client,
		Scheme:              scheme,
		tracer:              otel.Tracer("controller.common.configset"),
		logger:              logger.With("controller", "configset"),
		locker:              locker,
		system:              system,
		cfg:                 cfg,
```

with:

```go
func NewConfigSetReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg ConfigSetConfig, system handler.System, locker locker.Locker, watchdog *Watchdog) *ConfigSetReconciler {
	return &ConfigSetReconciler{
		Client:              client,
		Scheme:              scheme,
		tracer:              otel.Tracer("controller.common.configset"),
		logger:              logger.With("controller", "configset"),
		locker:              locker,
		system:              system,
		cfg:                 cfg,
		watchdog:            watchdog,
```

(Keep the rest of the field assignments intact.)

- [ ] **Step 5.2: Bracket Reconcile with Track**

In the same file, find the existing `Reconcile` method (around line 103). Add the watchdog call as the very first statement of the body, just after `r.logger.Debug(...)`:

```go
func (r *ConfigSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger.Debug("reconciling configset", "configset", req.Name)

	defer r.watchdog.Track("controller.common.configset", req.NamespacedName.String())()

	// Prevent a single stuck reconcile from blocking the worker indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
```

The `defer ...Track(...)()` pattern works because `Track` returns the cleanup func and we immediately call it via the trailing `()`, deferring that final call.

- [ ] **Step 5.3: Update existing tests that construct ConfigSetReconciler directly**

Search for all direct `&ConfigSetReconciler{` and `NewConfigSetReconciler(` callsites in tests and add `watchdog: nil,` (or pass `nil` as the new param). Nil is safe per the Track API.

Run:

```bash
grep -n "NewConfigSetReconciler(" /home/zach/go/src/github.com/zachfi/nodemanager/internal/controller/common/*.go /home/zach/go/src/github.com/zachfi/nodemanager/cmd/main.go
```

Expected hits to update:
- `cmd/main.go:270` — add `nil` for now; Task 9 wires the real watchdog.

Direct struct-literal constructions in tests don't set this field; they'll get the zero value (nil pointer), which is safe. No tests should need changes.

- [ ] **Step 5.4: Build + test**

```bash
cd /home/zach/go/src/github.com/zachfi/nodemanager && make build 2>&1 | tail -3
cd /home/zach/go/src/github.com/zachfi/nodemanager && make test 2>&1 | grep -E "^FAIL|^ok " | head -25
```

Expected: clean build; every package `ok`.

- [ ] **Step 5.5: Commit**

```bash
git add internal/controller/common/configset_controller.go cmd/main.go
git commit -m "$(cat <<'EOF'
feat(configset): bracket Reconcile with Watchdog.Track

Top-of-Reconcile heartbeat + in-flight registration; defer cleanup
on exit. nil-safe so existing test code continues to compile.

Refs znet/nodemanager#4.
EOF
)"
```

---

## Task 6: Wire Watchdog into ManagedNodeReconciler

**Files:**
- Modify: `internal/controller/common/managednode_controller.go`

Identical pattern to Task 5.

- [ ] **Step 6.1: Add field + constructor param**

Find the `ManagedNodeReconciler` struct in `internal/controller/common/managednode_controller.go` and add a `watchdog *Watchdog` field at the end (next to `cfg`/`notifier`/etc). Update `NewManagedNodeReconciler` (around line 77) to accept it as the final parameter:

```go
func NewManagedNodeReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg ManagedNodeConfig, system handler.System, locker locker.Locker, clientset kubernetes.Interface, agentVersion string, notifier notification.Notifier, watchdog *Watchdog) *ManagedNodeReconciler {
```

Set the field in the returned struct literal.

- [ ] **Step 6.2: Bracket Reconcile with Track**

In the same file, find `func (r *ManagedNodeReconciler) Reconcile(...)` (around line 102). Add as the first line of the body:

```go
	defer r.watchdog.Track("controller.common.managednode", req.NamespacedName.String())()
```

- [ ] **Step 6.3: Update callsite in main.go**

In `cmd/main.go`, the existing call at line 262 becomes:

```go
managedNodeReconciler := controller.NewManagedNodeReconciler(client, scheme, logger, cfg.ControllerConfig.ManagedNode, sys, locker, clientset, version, notifier, nil)
```

(Task 9 replaces `nil` with the real watchdog.)

- [ ] **Step 6.4: Build + test**

```bash
make build 2>&1 | tail -3
make test 2>&1 | grep -E "^FAIL|^ok " | head -25
```

Expected: clean.

- [ ] **Step 6.5: Commit**

```bash
git add internal/controller/common/managednode_controller.go cmd/main.go
git commit -m "$(cat <<'EOF'
feat(managednode): bracket Reconcile with Watchdog.Track

Mirror of the ConfigSet wiring. Same nil-safety contract.

Refs znet/nodemanager#4.
EOF
)"
```

---

## Task 7: Wire Watchdog into FreeBSD reconcilers

**Files:**
- Modify: `internal/controller/freebsd/poudriere_controller.go`
- Modify: `internal/controller/freebsd/jail_controller.go`

Cross-package call: freebsd reconcilers import `internal/controller/common` for the `*Watchdog` type. The package is already importable (it's where `commonv1` types live in some uses).

- [ ] **Step 7.1: Add field + constructor param to PoudriereReconciler**

In `internal/controller/freebsd/poudriere_controller.go`, find the `PoudriereReconciler` struct. Add:

```go
	watchdog *common.Watchdog
```

(Make sure `"github.com/zachfi/nodemanager/internal/controller/common"` is imported as `common`. If it isn't yet, add it.)

Update `NewPoudriereReconciler` (around line 89) to accept the watchdog as final param:

```go
func NewPoudriereReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg PoudriereConfig, system handler.System, watchdog *common.Watchdog) *PoudriereReconciler {
```

Set the field in the returned struct.

- [ ] **Step 7.2: Bracket PoudriereReconciler.Reconcile**

In the same file, find `func (r *PoudriereReconciler) Reconcile(...)` (around line 124). Add as the first line of the body:

```go
	defer r.watchdog.Track("controller.freebsd.poudriere", req.NamespacedName.String())()
```

- [ ] **Step 7.3: Same treatment for JailReconciler**

In `internal/controller/freebsd/jail_controller.go`, find the `JailReconciler` struct and add a `watchdog *common.Watchdog` field. Update `NewJailReconciler` (around line 80) to take the watchdog as final param:

```go
func NewJailReconciler(ctx context.Context, client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg JailConfig, system handler.System, lkr locker.Locker, watchdog *common.Watchdog) (*JailReconciler, error) {
```

Set the field in the returned struct.

Find `Reconcile` (around line 109). Add as the first line of the body:

```go
	defer r.watchdog.Track("controller.freebsd.jail", req.NamespacedName.String())()
```

- [ ] **Step 7.4: Update main.go callsites**

In `cmd/main.go`, find every call to `freebsd.NewPoudriereReconciler` and `freebsd.NewJailReconciler` and add a final `nil` arg (Task 9 replaces with the real watchdog):

```bash
grep -n "NewPoudriereReconciler\|NewJailReconciler" /home/zach/go/src/github.com/zachfi/nodemanager/cmd/main.go
```

For each match, add `, nil` before the closing paren.

- [ ] **Step 7.5: Build + test**

```bash
make build 2>&1 | tail -3
make test 2>&1 | grep -E "^FAIL|^ok " | head -25
```

Expected: clean.

- [ ] **Step 7.6: Commit**

```bash
git add internal/controller/freebsd/poudriere_controller.go \
        internal/controller/freebsd/jail_controller.go \
        cmd/main.go
git commit -m "$(cat <<'EOF'
feat(freebsd): bracket Poudriere + Jail Reconciles with Watchdog.Track

Cross-package call into internal/controller/common.Watchdog. Same
nil-safety contract; production wiring lands in cmd/main.go.

Refs znet/nodemanager#4.
EOF
)"
```

---

## Task 8: Register Watchdog in `cmd/main.go`

**Files:**
- Modify: `cmd/main.go`

- [ ] **Step 8.1: Construct the Watchdog and pass to reconcilers**

In `cmd/main.go`, immediately after `controller.SetBuildInfo(...)` (around line 248) and before the notification server block, add:

```go
	watchdog := controller.NewWatchdog(
		cfg.ControllerConfig.Watchdog,
		hostname,
		cfg.ControllerConfig.ConfigSet.ReconcilePeriod,
		logger,
	)
	if err := mgr.Add(watchdog); err != nil {
		setupLog.Error(err, "unable to add watchdog runnable")
		os.Exit(1)
	}
```

- [ ] **Step 8.2: Replace the `nil` placeholders in reconciler calls**

Find each `nil` placeholder added in Tasks 5-7 and replace with `watchdog`:

- `controller.NewManagedNodeReconciler(..., notifier, nil)` → `..., notifier, watchdog)`
- `controller.NewConfigSetReconciler(..., locker, nil)` → `..., locker, watchdog)`
- `freebsd.NewPoudriereReconciler(..., sys, nil)` → `..., sys, watchdog)`
- `freebsd.NewJailReconciler(ctx, ..., lkr, nil)` → `..., lkr, watchdog)`

Use grep to verify only zero `, nil)` calls into these constructors remain:

```bash
grep -E "NewManagedNodeReconciler|NewConfigSetReconciler|NewPoudriereReconciler|NewJailReconciler" /home/zach/go/src/github.com/zachfi/nodemanager/cmd/main.go
```

- [ ] **Step 8.3: Build + test**

```bash
make build 2>&1 | tail -3
make test 2>&1 | grep -E "^FAIL|^ok " | head -25
```

Expected: clean build; every package `ok`.

- [ ] **Step 8.4: Commit**

```bash
git add cmd/main.go
git commit -m "$(cat <<'EOF'
feat(main): construct and register the Watchdog runnable

Builds the Watchdog from cfg, passes hostname and the ConfigSet
reconcile period (for preflight), registers via mgr.Add so its Start
gets ctx-cancelled cleanly on manager shutdown.

Closes znet/nodemanager#4.
EOF
)"
```

---

## Task 9: Bounded OTLP exporter + `-tracing.enabled` (issue #9)

**Files:**
- Modify: `cmd/main.go`

Issue #9: bound the exporter, bound the processor, gate on `cfg.Tracing.Enabled`, shorten shutdown.

- [ ] **Step 9.1: Locate the tracing setup function**

```bash
grep -n "otlptracegrpc.New\|NewBatchSpanProcessor\|tracerProvider.Shutdown" /home/zach/go/src/github.com/zachfi/nodemanager/cmd/main.go
```

Expected: hits at the trace setup function (around lines 377-396).

- [ ] **Step 9.2: Gate setup on cfg.Tracing.Enabled**

Read the function containing `otlptracegrpc.New` (around lines 360-400). At the top of that function (after any param validation, before the resource construction), add an early-return when tracing is disabled:

```go
	if !cfg.ControllerConfig.Tracing.Enabled {
		logger.Info("tracing disabled via -tracing.enabled=false")
		return func() {}, nil
	}
```

(The function name and signature vary; the early-return should match its existing return type — a `func()` cleanup and `error`. If the signature is different, adapt accordingly. The cleanup must be a no-op since no provider was created.)

- [ ] **Step 9.3: Bound the exporter**

Replace:

```go
	traceExporter, err := otlptracegrpc.New(ctx)
```

with:

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

- [ ] **Step 9.4: Bound the batch processor**

Replace:

```go
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(traceExporter)),
```

with:

```go
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(traceExporter,
			sdktrace.WithBatchTimeout(2*time.Second),
			sdktrace.WithExportTimeout(5*time.Second),
		)),
```

- [ ] **Step 9.5: Shorten shutdown ctx**

Replace:

```go
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
```

with:

```go
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
```

- [ ] **Step 9.6: Build + test**

```bash
make build 2>&1 | tail -3
make test 2>&1 | grep -E "^FAIL|^ok " | head -25
```

Expected: clean.

- [ ] **Step 9.7: Smoke test the disable flag**

Run:

```bash
./bin/nodemanager -tracing.enabled=false -h 2>&1 | head -5
```

Expected: usage prints; no panic. (We don't run the full agent here — flag parsing alone validates the wiring.)

- [ ] **Step 9.8: Commit**

```bash
git add cmd/main.go
git commit -m "$(cat <<'EOF'
feat(tracing): bounded OTLP exporter + -tracing.enabled kill switch

Per-export timeout 3s; retry envelope 3s; batch processor export 5s;
shutdown ctx 2s. Worst-case worker goroutine occupancy on a failing
batch ~6s, down from ~60s.

When -tracing.enabled=false, skip exporter + processor + provider
setup entirely; global TracerProvider remains the SDK's noop so every
r.tracer.Start(...) call keeps working. Recovery escape hatch when
the trace backend is what nodemanager is trying to fix.

Closes znet/nodemanager#9.
EOF
)"
```

---

## Task 10: `NodeManagerReconcileStuck` + `NodeManagerAgentRestartLoop` alerts

**Files:**
- Modify: `monitoring/alerts/nodemanager.libsonnet`

- [ ] **Step 10.1: Find the insertion point**

```bash
grep -n "NodeManagerServiceStartLoop\|Poudriere build metrics" /home/zach/go/src/github.com/zachfi/nodemanager/monitoring/alerts/nodemanager.libsonnet | head -5
```

Insert after `NodeManagerServiceStartLoop`'s closing `},` and before the `// ── Poudriere build metrics ───` section comment.

- [ ] **Step 10.2: Insert the two alerts**

Add this block:

```jsonnet
    // ── Agent health (watchdog) ──────────────────────────────────────────────

    {
      alert: 'NodeManagerReconcileStuck',
      expr: |||
        nodemanager_reconcile_in_flight_duration_seconds > 0
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'reconcile stuck on {{ $labels.node }} ({{ $labels.controller }} / {{ $labels.key }}).',
        description: |||
          A reconcile in controller {{ $labels.controller }} for {{ $labels.key }} has
          been in-flight on {{ $labels.node }} for {{ $value | humanizeDuration }}.
          Likely an uncancellable syscall, deadlock, or upstream service hang.
          Before restarting: capture goroutine dump via /debug/pprof/goroutine?debug=2.
        |||,
      },
    },

    {
      alert: 'NodeManagerAgentRestartLoop',
      expr: |||
        changes(process_start_time_seconds{job=~".*nodemanager.*"}[30m]) > 3
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'nodemanager on {{ $labels.instance }} has restarted >3 times in 30 minutes.',
        description: |||
          Likely the watchdog firing (exit code 74 — check supervisor logs for
          "watchdog: stale; exiting"). Investigate why Reconcile is not running:
          controller-runtime reflector health, apiserver connectivity, agent kubeconfig.
        |||,
      },
    },
```

Match the existing indentation (4 leading spaces for the alert object).

- [ ] **Step 10.3: Render via project-local jsonnet**

```bash
cd /home/zach/go/src/github.com/zachfi/nodemanager && make -s mixin-generate 2>/dev/null | jq '.groups[0].rules[] | select(.alert == "NodeManagerReconcileStuck" or .alert == "NodeManagerAgentRestartLoop") | .alert'
```

Expected: two lines:
```
"NodeManagerReconcileStuck"
"NodeManagerAgentRestartLoop"
```

- [ ] **Step 10.4: Lint**

```bash
make mixin-lint 2>&1 | tail -3
```

Expected: `mixin-lint: OK`. If formatting drift on the edited file, run `make mixin-fmt` and re-stage.

- [ ] **Step 10.5: Commit**

```bash
git add monitoring/alerts/nodemanager.libsonnet
git commit -m "$(cat <<'EOF'
feat(alerts): agent-health alerts for the watchdog signals

NodeManagerReconcileStuck fires when the in-flight gauge has any
series for 5m. Annotations point operators at /debug/pprof/goroutine
for capture before restart.

NodeManagerAgentRestartLoop fires on >3 process restarts in 30m as
a Prometheus-observable surrogate for "watchdog firing repeatedly"
(the exit itself isn't directly observable).
EOF
)"
```

---

## Task 11: Agent-health dashboard

**Files:**
- Create: `monitoring/dashboards/nodemanager-agent-health.libsonnet`

- [ ] **Step 11.1: Read the existing dashboard for structure**

```bash
head -60 /home/zach/go/src/github.com/zachfi/nodemanager/monitoring/dashboards/nodemanager-configset.libsonnet
```

Match its top-level structure (imports, panels list, datasource references).

- [ ] **Step 11.2: Create the new dashboard**

Create `monitoring/dashboards/nodemanager-agent-health.libsonnet`:

```jsonnet
local g = import 'grafonnet/grafana.libsonnet';

{
  grafanaDashboards+:: {
    'nodemanager-agent-health.json':
      g.dashboard.new(
        'nodemanager / Agent Health',
        uid='nodemanager-agent-health',
        editable=true,
        time_from='now-6h',
      )
      .addPanel(
        g.statPanel.new(
          'Agent uptime',
          datasource='$datasource',
          reducerFunction='lastNotNull',
          unit='s',
        )
        .addTarget(g.prometheus.target(
          'time() - process_start_time_seconds{job=~".*nodemanager.*"}',
          legendFormat='{{instance}}',
        ))
        .addThreshold({ color: 'red', value: 0 })
        .addThreshold({ color: 'yellow', value: 600 })
        .addThreshold({ color: 'green', value: 3600 }),
        gridPos={ h: 6, w: 6, x: 0, y: 0 },
      )
      .addPanel(
        g.timeseriesPanel.new(
          'In-flight reconciles (age)',
          datasource='$datasource',
          unit='s',
        )
        .addTarget(g.prometheus.target(
          'nodemanager_reconcile_in_flight_duration_seconds',
          legendFormat='{{node}} / {{controller}} / {{key}}',
        )),
        gridPos={ h: 6, w: 12, x: 6, y: 0 },
      )
      .addPanel(
        g.timeseriesPanel.new(
          'Reconcile rate (5m)',
          datasource='$datasource',
          unit='ops',
        )
        .addTarget(g.prometheus.target(
          'sum by (node) (rate(controller_runtime_reconcile_total{job=~".*nodemanager.*"}[5m]))',
          legendFormat='{{node}}',
        )),
        gridPos={ h: 6, w: 6, x: 18, y: 0 },
      ),
  },
}
```

If the existing dashboard uses a different import or panel-builder API (e.g. raw jsonnet rather than grafonnet), match that style instead — copy from `nodemanager-configset.libsonnet`. The four-panel set described in the spec stays the same; only the rendering pattern changes.

- [ ] **Step 11.3: Render + lint**

```bash
make -s mixin-generate 2>/dev/null | jq 'has("grafanaDashboards") | not' 2>&1
```

(Mixin-generate is alerts-only by default. Dashboard rendering may be a separate target. Run:)

```bash
ls monitoring/dashboards.libsonnet 2>&1
```

If there's a top-level `dashboards.libsonnet`, register the new file there. If not, the dashboard sits unwired until an operator includes it from `znet/deployment_tools`. Either way, the file must parse:

```bash
cd /home/zach/go/src/github.com/zachfi/nodemanager/monitoring && ../bin/jsonnet-v0.20.0 -J vendor -J . dashboards/nodemanager-agent-health.libsonnet 2>&1 | head -5
```

Expected: valid JSON (or jsonnet rendering errors that you fix before continuing).

```bash
make mixin-lint 2>&1 | tail -3
```

Expected: `mixin-lint: OK`.

- [ ] **Step 11.4: Commit**

```bash
git add monitoring/dashboards/nodemanager-agent-health.libsonnet
git commit -m "$(cat <<'EOF'
feat(dashboards): nodemanager agent-health dashboard

Three-panel dashboard: agent uptime, in-flight reconcile ages,
reconcile rate. Distinct from the ConfigSet rollout dashboard so the
agent-operator audience has a focused view.
EOF
)"
```

---

## Task 12: Docs row for the new metric

**Files:**
- Modify: `docs/monitoring/metrics.md`

- [ ] **Step 12.1: Insert the new row**

Find the existing "Services" or "Packages" section in `docs/monitoring/metrics.md`. After the `nodemanager_service_operations_total` row, add an "Agent health" subsection with one row:

```
### Agent health

| Metric | Labels | Description |
|---|---|---|
| `nodemanager_reconcile_in_flight_duration_seconds` | `node`, `controller`, `key` | Age (seconds) of an in-flight Reconcile that has exceeded `watchdog.slow-threshold`. Empty in steady state; bounded handful of series during incidents. Reset and rebuilt on every watchdog tick (~1m). |
```

- [ ] **Step 12.2: Commit**

```bash
git add docs/monitoring/metrics.md
git commit -m "docs(metrics): add row for nodemanager_reconcile_in_flight_duration_seconds"
```

---

## Task 13: Final verification + PR

**Files:** none

- [ ] **Step 13.1: Lint, build, test**

```bash
make build 2>&1 | tail -3
make test 2>&1 | grep -E "^FAIL|^ok " | head -25
make mixin-lint 2>&1 | tail -3
go vet ./... 2>&1 | tail -3
```

Expected: all four pass / `mixin-lint: OK`. `make lint` is broken on main (pre-existing golangci-lint v1.54.2 / Go 1.24 incompatibility — backlog item); skip it and call out in the PR description.

- [ ] **Step 13.2: Verify commit history**

```bash
git log --pretty='format:%h %G? %s' feat/agent-resilience-pass ^main
```

Expected: 13 commits, all signed (`G`). Order: spec → spec extension → WatchdogConfig+TracingConfig → Watchdog struct → metric → Watchdog ticker → ConfigSet wiring → ManagedNode wiring → freebsd wiring → main wiring → tracing → alerts → dashboard → metrics docs.

- [ ] **Step 13.3: Push and open PR**

```bash
git push -u origin feat/agent-resilience-pass 2>&1 | tail -5
fj -H code.znet pr create -r znet/nodemanager --base main --head feat/agent-resilience-pass "agent resilience pass: watchdog + best-effort traces" --body "$(cat <<'EOF'
Closes #4, closes #9.

## Summary

- **Watchdog (#4):** two independent signals.
  - Entry heartbeat (\`atomic.Int64\`) bumped at top of every Reconcile in all four controllers (ConfigSet, ManagedNode, Poudriere, Jail). Stale > 10m → \`exit(74)\` for supervisor restart.
  - In-flight tracker (\`sync.Map\`) registered/unregistered around each Reconcile body. Entries older than 15m surface as \`nodemanager_reconcile_in_flight_duration_seconds{node, controller, key}\` + WARN log. No fatal exit — legitimate slow operations exist (jail provisions).
  - Preflight: \`watchdog.stale-threshold > 0\` requires \`configset.reconcile-period > 0\`; otherwise refuses to start to prevent crash-loops on idle fleets.
- **Trace best-effort (#9):** OTLP exporter bounded (per-export 3s, retry envelope 3s); BatchSpanProcessor capped (export-timeout 5s); shutdown ctx 5s → 2s; \`-tracing.enabled\` kill switch falls back to the SDK noop TracerProvider without changing any \`r.tracer.Start(...)\` call.
- **Mixin coverage (#3):** \`NodeManagerReconcileStuck\` + \`NodeManagerAgentRestartLoop\` alerts; new \`nodemanager / Agent Health\` dashboard.

Spec: \`docs/superpowers/specs/2026-05-27-agent-resilience-pass-design.md\`
Plan: \`docs/superpowers/plans/2026-05-27-agent-resilience-pass.md\`

## Test plan

- [x] \`make build\` clean.
- [x] \`make test\` green across all 18 packages, including 9 new Ginkgo specs in \`internal/controller/common/watchdog_test.go\` (Track API basics, startup grace, stale-exit, slow-gauge, disabled paths, preflight rejection, concurrent Track safety).
- [x] \`make mixin-lint\` OK.
- [x] \`go vet ./...\` clean.
- [ ] \`make lint\` blocked by pre-existing golangci-lint v1.54.2 / Go 1.24 incompatibility — unrelated.
- [ ] Single-host smoke: deploy to one BSD host, observe heartbeat metric, kill apiserver connectivity, observe \`exit(74)\` within 10m and supervisor restart.
- [ ] Trace-disabled smoke: deploy with \`-tracing.enabled=false\`, confirm no OTLP dial attempts in goroutine dump.

## Out of scope

- Auto-recovery from a wedged reconcile (the watchdog only decides when; supervisor handles recovery).
- Per-controller \`MaxConcurrentReconciles\` tuning.
- Span drop counters as a Prometheus metric (no public Go API surface).
EOF
)" 2>&1 | tail -3
```

Stop here and confirm with the user before merging.

---

## Self-Review Notes

- **Spec coverage:**
  - Part 1 watchdog: Tasks 1, 2, 4, 5, 6, 7, 8 (config, struct, ticker, four controller wirings, main wiring).
  - Part 2 tracing: Tasks 1, 9 (config + main).
  - Part 3 mixin: Tasks 10, 11, 12 (alerts, dashboard, metric docs).
  - In-flight metric: Task 3.
- **Type consistency:** `*Watchdog` everywhere, `WatchdogConfig` struct name consistent across tasks. `Track(controller, name string) func()` signature locked at top of plan and used identically in every wiring task. Metric label set `{node, controller, key}` consistent between Task 3 (definition), Task 4 (population), Task 10 (alerts), Task 11 (dashboard), Task 12 (docs).
- **TDD discipline:** Tasks 2 and 4 follow strict failing-test-first. Tasks 5-7 are mechanical bracket additions — covered indirectly by the full-suite run, plus the existing tests that already exercise these reconcilers don't false-fail because the watchdog field is nil-safe.
- **Defaults:** stale=10m, slow=15m, tracing.enabled=true (per spec).
- **Out-of-scope respected:** no circuit breaker on exporter, no periodic self-list, no per-controller concurrency tuning.
