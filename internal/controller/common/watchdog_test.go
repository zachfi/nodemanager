/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package common

import (
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
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
	w := NewWatchdog(WatchdogConfig{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
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
	w := NewWatchdog(WatchdogConfig{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
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
	w := NewWatchdog(WatchdogConfig{StaleThreshold: time.Minute, SlowThreshold: time.Minute}, "test-node", time.Minute, logger)
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

