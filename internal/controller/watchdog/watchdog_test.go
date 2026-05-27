/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package watchdog

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Test-only accessors on Watchdog. Defined in the test file so the production
// package has no symbols that exist solely for tests (avoids the `unused`
// linter flagging them).

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

func TestWatchdog_NilReceiverNoOp(t *testing.T) {
	var w *Watchdog
	done := w.Track("ctrl", "ns/name")
	if done == nil {
		t.Fatal("expected non-nil done func from nil receiver")
	}
	done() // must not panic
}

func TestWatchdog_TrackAndClear(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	w := New(Config{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
	if got := w.inFlightCount(); got != 0 {
		t.Fatalf("expected 0 in-flight, got %d", got)
	}

	done := w.Track("ctrl", "ns/name")
	if got := w.inFlightCount(); got != 1 {
		t.Fatalf("expected 1 in-flight after Track, got %d", got)
	}

	done()
	if got := w.inFlightCount(); got != 0 {
		t.Fatalf("expected 0 in-flight after done(), got %d", got)
	}
}

func TestWatchdog_HeartbeatBumped(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	w := New(Config{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
	if got := w.lastHeartbeatNanos(); got != 0 {
		t.Fatalf("expected 0 heartbeat before any Track, got %d", got)
	}

	done := w.Track("ctrl", "ns/name")
	defer done()
	if got := w.lastHeartbeatNanos(); got <= 0 {
		t.Fatalf("expected positive heartbeat after Track, got %d", got)
	}
}

func TestWatchdog_ConcurrentTrack(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	w := New(Config{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			done := w.Track("ctrl", "key/"+string(rune(i)))
			done()
		}(i)
	}
	wg.Wait()
	if got := w.inFlightCount(); got != 0 {
		t.Fatalf("expected 0 in-flight after all goroutines complete, got %d", got)
	}
}

func TestWatchdog_PreflightRejectsStaleWithoutReconcilePeriod(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	w := New(Config{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", 0, logger)
	w.exit = func(int) {}
	w.interval = 10 * time.Millisecond

	err := w.Start(t.Context())
	if err == nil {
		t.Fatal("expected error from Start when stale>0 and reconcilePeriod==0")
	}
	if !strings.Contains(err.Error(), "watchdog.stale-threshold") {
		t.Fatalf("error must mention watchdog.stale-threshold; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "configset.reconcile-period") {
		t.Fatalf("error must mention configset.reconcile-period; got %q", err.Error())
	}
}

func TestWatchdog_StartupGraceNoExit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	exitCalls := make(chan int, 1)
	w := New(
		Config{StaleThreshold: 50 * time.Millisecond, SlowThreshold: time.Minute},
		"test-node", time.Minute, logger,
	)
	w.exit = func(c int) { exitCalls <- c }
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	select {
	case c := <-exitCalls:
		t.Fatalf("watchdog must not exit during startup grace; got exit(%d)", c)
	default:
	}
}

func TestWatchdog_ExitsOnStaleHeartbeat(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	exitCalls := make(chan int, 1)
	w := New(
		Config{StaleThreshold: 30 * time.Millisecond, SlowThreshold: time.Minute},
		"test-node", time.Minute, logger,
	)
	w.exit = func(c int) { exitCalls <- c }
	w.interval = 10 * time.Millisecond
	w.heartbeat.Store(time.Now().Add(-time.Hour).UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	select {
	case c := <-exitCalls:
		if c != 74 {
			t.Fatalf("expected exit code 74; got %d", c)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("watchdog did not call exit within 300ms of a stale heartbeat")
	}
}

func TestWatchdog_SlowGaugeSetAndCleared(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	w := New(
		Config{StaleThreshold: 0, SlowThreshold: 20 * time.Millisecond},
		"test-node", time.Minute, logger,
	)
	w.exit = func(int) {}
	w.interval = 10 * time.Millisecond
	w.heartbeat.Store(time.Now().UnixNano())

	// Simulate a stuck reconcile: Track but never call cleanup.
	done := w.Track("ctrl", "ns/badd")
	t.Cleanup(done)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	startErrCh := make(chan error, 1)
	go func() { startErrCh <- w.Start(ctx) }()

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if gaugeValue(reconcileInFlightDuration, "test-node", "ctrl", "ns/badd") > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := gaugeValue(reconcileInFlightDuration, "test-node", "ctrl", "ns/badd"); got == 0 {
		t.Fatal("expected gauge to be populated for stuck reconcile, got 0")
	}

	// Resolve the stuck reconcile; gauge should be reset on the next tick.
	done()
	deadline = time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if gaugeValue(reconcileInFlightDuration, "test-node", "ctrl", "ns/badd") == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := gaugeValue(reconcileInFlightDuration, "test-node", "ctrl", "ns/badd"); got != 0 {
		t.Fatalf("expected gauge cleared after reconcile resolved, got %v", got)
	}

	cancel()
	if err := <-startErrCh; err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
}

func TestWatchdog_DisabledStaleNeverExits(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	exitCalls := make(chan int, 1)
	w := New(
		Config{StaleThreshold: 0, SlowThreshold: time.Minute},
		"test-node", time.Minute, logger,
	)
	w.exit = func(c int) { exitCalls <- c }
	w.interval = 10 * time.Millisecond
	w.heartbeat.Store(time.Now().Add(-time.Hour).UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	select {
	case c := <-exitCalls:
		t.Fatalf("watchdog must not exit when stale=0; got exit(%d)", c)
	default:
	}
}
