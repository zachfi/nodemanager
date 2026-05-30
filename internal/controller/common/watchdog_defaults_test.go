package common

import (
	"context"
	"flag"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWatchdog_ShippedDefaultsStartCleanly is the regression guard for #15.
//
// In production the agent runs entirely on flag defaults (only the OTEL
// environment arguments differ), so the shipped defaults must be mutually
// consistent and let the watchdog start. Before the fail-open fix, the default
// pair — watchdog.stale-threshold=10m together with configset.reconcile-period=0
// — made Start() return a preflight error, so every default-config agent exited
// at startup and was restarted into a crash loop (fleet-wide outage on the
// v0.17.0 auto-upgrade).
//
// This drives the real registered defaults through the exact construction
// main.go uses (NewWatchdog(cfg.Watchdog, host, cfg.ConfigSet.ReconcilePeriod,
// ...)) and asserts the agent starts. The fail-open mechanism itself — stale
// check disabled, never exits — is covered by the watchdog package's
// TestWatchdog_StaleWithoutReconcilePeriodFailsOpen.
func TestWatchdog_ShippedDefaultsStartCleanly(t *testing.T) {
	var cfg ControllerConfig
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg.RegisterFlagsAndApplyDefaults("controller", fs)
	require.NoError(t, fs.Parse(nil)) // no overrides — apply the shipped defaults

	// The exact default pair from #15. A positive stale-threshold with a zero
	// reconcile-period is the condition that bricked agents; if a future change
	// alters either default, revisit this test (and the fail-open contract)
	// deliberately rather than letting it silently drift.
	require.Positive(t, cfg.Watchdog.StaleThreshold, "watchdog.stale-threshold default should be > 0")
	require.Zero(t, cfg.ConfigSet.ReconcilePeriod, "configset.reconcile-period default should be 0 (event-driven)")

	w := NewWatchdog(cfg.Watchdog, "test-node", cfg.ConfigSet.ReconcilePeriod, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.NoError(t, w.Start(ctx), "agent must start on shipped defaults (fail open, not brick)")
}
