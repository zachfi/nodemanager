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

// NewWatchdog builds a Watchdog with production defaults (now=time.Now,
// interval=1m). The exit func defaults to os.Exit and is set inside Start.
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
	now := w.now()
	w.heartbeat.Store(now.UnixNano())
	key := controller + "/" + name
	w.inFlight.Store(key, now)
	return func() {
		w.inFlight.Delete(key)
	}
}
