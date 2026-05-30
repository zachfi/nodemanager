/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package common

import (
	"context"
	"time"

	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/nodemanager/internal/controller/watchdog"
)

// Watchdog is a re-export alias so existing call sites in this package
// continue to compile without change. The canonical implementation lives in
// internal/controller/watchdog; the alias allows the freebsd package to import
// that sub-package directly and break the common→freebsd→common import cycle.
type Watchdog = watchdog.Watchdog

// WatchdogConfig is a re-export alias for watchdog.Config.
type WatchdogConfig = watchdog.Config

// NewWatchdog is a convenience wrapper so existing call sites in this package
// and cmd/ do not need updating. It delegates to watchdog.New.
func NewWatchdog(cfg WatchdogConfig, nodeName string, reconcilePeriod time.Duration, logger *slog.Logger) *Watchdog {
	return watchdog.New(cfg, nodeName, reconcilePeriod, logger)
}

// ManagedNodeProbe is a re-export wrapper so cmd/ can build a watchdog
// connectivity probe without importing the watchdog sub-package directly. It
// delegates to watchdog.ManagedNodeProbe.
func ManagedNodeProbe(reader client.Reader, name, namespace string) func(context.Context) error {
	return watchdog.ManagedNodeProbe(reader, name, namespace)
}
