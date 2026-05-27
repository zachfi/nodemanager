/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package watchdog provides the Watchdog type used by all reconcilers to
// detect stale / stuck Reconcile loops.  It is kept in its own package so
// both the common and freebsd controller packages can import it without
// creating a cycle.
package watchdog

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Config controls the agent self-watchdog: catches "Reconcile loop
// stopped" by exiting the process, and "Reconcile stuck in-flight" by
// surfacing a metric + WARN log. Both signals are independently disable-able.
type Config struct {
	// StaleThreshold: exit if no Reconcile entry in this duration. 0 disables.
	StaleThreshold time.Duration `json:"staleThreshold,omitempty"`
	// SlowThreshold: WARN + metric if a single Reconcile has been in-flight
	// longer than this. 0 disables.
	SlowThreshold time.Duration `json:"slowThreshold,omitempty"`
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&c.StaleThreshold, prefix+".stale-threshold", 10*time.Minute, "Exit the agent (code 74) if no Reconcile entry occurs in this duration. 0 disables. Requires configset.reconcile-period > 0 when enabled.")
	f.DurationVar(&c.SlowThreshold, prefix+".slow-threshold", 15*time.Minute, "Surface a WARN log and metric series when a single Reconcile has been in-flight longer than this. 0 disables.")
}

var (
	// reconcileInFlightDuration exposes the age of any Reconcile call that
	// has been running longer than Config.SlowThreshold. Empty in steady
	// state; a small handful of series during incidents. Watchdog ticker
	// resets the gauge each tick and re-populates from current in-flight
	// scan, so series for resolved reconciles disappear.
	reconcileInFlightDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nodemanager_reconcile_in_flight_duration_seconds",
		Help: "Age of an in-flight Reconcile call exceeding the watchdog slow threshold.",
	}, []string{"node", "controller", "key"})
)

func init() {
	metrics.Registry.MustRegister(reconcileInFlightDuration)
}

// Watchdog owns the heartbeat and in-flight reconcile tracker. Reconcilers
// call Track at the top of Reconcile; the returned cleanup func is deferred
// to clear the in-flight entry on exit.
//
// A nil *Watchdog is safe to call Track on — returns a no-op cleanup —
// so reconcilers in unit tests may leave the field unset.
type Watchdog struct {
	cfg             Config
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

// New builds a Watchdog with production defaults (now=time.Now,
// interval=1m). The exit func defaults to os.Exit and is set inside Start.
func New(cfg Config, nodeName string, reconcilePeriod time.Duration, logger *slog.Logger) *Watchdog {
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
	now := w.now()
	w.heartbeat.Store(now.UnixNano())
	key := controller + "/" + name
	w.inFlight.Store(key, now)
	return func() {
		w.inFlight.Delete(key)
	}
}

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

	rawExit := w.exit
	if rawExit == nil {
		rawExit = os.Exit
	}

	// Wrap exitFn so that after it fires the ticker loop also stops — in
	// production os.Exit terminates the process; in tests the injected func
	// merely records the call and returns, so we need an explicit stop signal.
	exitDone := make(chan struct{})
	exitFn := func(code int) {
		rawExit(code)
		// Only reached in tests (os.Exit never returns).
		select {
		case <-exitDone: // already closed
		default:
			close(exitDone)
		}
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
		case <-exitDone:
			return nil
		case <-ticker.C:
			w.tick(exitFn)
		}
	}
}

// tick runs the per-interval check. The exit func is threaded through so
// tests can capture it without touching os.Exit.
func (w *Watchdog) tick(exitFn func(int)) {
	now := w.now()

	// Signal 1: heartbeat staleness.
	if w.cfg.StaleThreshold > 0 {
		hb := w.heartbeat.Load()
		if hb != 0 {
			since := now.Sub(time.Unix(0, hb))
			if since > w.cfg.StaleThreshold {
				w.logger.Error("watchdog: no reconcile entry in threshold; exiting for supervisor restart",
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
}

// splitTrackKey reverses the format used in Track: "controller/key".
func splitTrackKey(s string) (controller, name string) {
	controller, name, _ = strings.Cut(s, "/")
	return controller, name
}
