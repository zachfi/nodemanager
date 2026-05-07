package limits

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestProfile_ApplyTo_SetsMaxConcurrentAndLimiter(t *testing.T) {
	p := Profile{
		MaxConcurrent: 3,
		BaseDelay:     time.Second,
		MaxDelay:      time.Minute,
		BucketEvery:   5 * time.Second,
		BucketBurst:   1,
	}
	opts := controller.Options{}
	p.ApplyTo(&opts)

	require.Equal(t, 3, opts.MaxConcurrentReconciles)
	require.NotNil(t, opts.RateLimiter, "rate limiter must be set")
}

func TestProfile_ApplyTo_PreservesUnrelatedOptionsFields(t *testing.T) {
	// CacheSyncTimeout is not touched by ApplyTo; verify it survives.
	opts := controller.Options{
		CacheSyncTimeout: 7 * time.Second,
	}
	Default.ApplyTo(&opts)
	require.Equal(t, 7*time.Second, opts.CacheSyncTimeout)
}

func TestProfile_RateLimiter_NoBucket_OnlyExponentialApplies(t *testing.T) {
	// With BucketEvery=0, RateLimiter() returns a bare exponential
	// limiter: When() grows as BaseDelay * 2^failures, bounded by
	// MaxDelay, with no token-bucket dominance.  Two rapid calls should
	// stay in the exponential range (1ms, 2ms, …) and never hit a
	// bucket-imposed second-scale delay.
	p := Profile{
		MaxConcurrent: 1,
		BaseDelay:     10 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BucketEvery:   0,
	}
	rl := p.RateLimiter()
	req := reconcile.Request{}

	first := rl.When(req)
	second := rl.When(req)
	// Exponential: failures=1 → 10ms, failures=2 → 20ms.
	require.Equal(t, 10*time.Millisecond, first)
	require.Equal(t, 20*time.Millisecond, second)
}

func TestProfile_RateLimiter_BucketDominatesOnRapidCalls(t *testing.T) {
	// With a tight bucket (1 token / second, burst 1) and a tiny
	// exponential delay, the second back-to-back call is delayed by
	// the token-bucket refill, not the exponential clock.  This is the
	// posture that prevents a hot-looping reconciler from starving
	// siblings.
	p := Profile{
		MaxConcurrent: 1,
		BaseDelay:     10 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BucketEvery:   time.Second,
		BucketBurst:   1,
	}
	rl := p.RateLimiter()
	req := reconcile.Request{}

	_ = rl.When(req) // first call consumes the burst token
	delay := rl.When(req)
	// Bucket needs ~1s to refill; exponential is at most 20ms; MaxOf
	// takes the larger.
	require.Greater(t, delay, 500*time.Millisecond, "bucket must dominate the exponential")
	require.LessOrEqual(t, delay, time.Second+50*time.Millisecond, "wait is bounded by BucketEvery")
}

func TestPredefinedProfiles_HaveSensibleValues(t *testing.T) {
	// Smoke check: every predefined profile is single-concurrent and has
	// a non-zero failure backoff cap.  Catches accidental zero-value
	// regressions in the package-level vars.
	for name, p := range map[string]Profile{
		"Default":     Default,
		"LongRunning": LongRunning,
		"FastReact":   FastReact,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, 1, p.MaxConcurrent, "MaxConcurrent should be 1")
			require.Greater(t, p.BaseDelay, time.Duration(0), "BaseDelay must be positive")
			require.Greater(t, p.MaxDelay, p.BaseDelay, "MaxDelay must exceed BaseDelay")
		})
	}
}

func TestPredefinedProfiles_DistinctRateShape(t *testing.T) {
	// Each preset is meant to encode a different posture.  Assert the
	// values that distinguish them so a future edit that accidentally
	// homogenises two profiles trips a test.
	require.Equal(t, 10*time.Second, Default.BucketEvery, "Default uses a 10s steady-state bucket")
	require.Equal(t, 30*time.Second, LongRunning.BucketEvery, "LongRunning uses a 30s steady-state bucket")
	require.Equal(t, time.Duration(0), FastReact.BucketEvery, "FastReact has no steady-state bucket")

	// LongRunning's bucket is strictly stricter than Default's (longer
	// interval = fewer reconciles per minute).
	require.Greater(t, LongRunning.BucketEvery, Default.BucketEvery)
}
