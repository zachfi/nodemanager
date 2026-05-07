// Package limits centralises per-controller rate-limiter and concurrency
// profiles so every reconciler in nodemanager shares a consistent posture
// toward (a) backing off on errors instead of hammering the Kubernetes
// API and (b) capping its workqueue throughput so one busy reconciler
// can't starve siblings sharing the same controller-runtime manager.
//
// Why centralise:
//   - Each controller previously rolled its own rate limiter inline in
//     SetupWithManager, with subtly different backoff bounds and bucket
//     rates.  One reconciler (Poudriere) had no limiter at all and
//     inherited the controller-runtime default (5 concurrent reconciles,
//     5ms→1000s exponential, no bucket) — wildly out of step with the
//     rest of the controllers in the same process.
//   - A future reconciler should inherit the same defaults without the
//     author needing to re-derive what "reasonable" looks like.  Picking
//     a profile is now a one-line decision.
//
// Two axes determine which profile fits a controller:
//
//   - How fast does it need to react to bursts?  Watching a fan-out of
//     Secret/ConfigMap changes wants quick drain (FastReact); a
//     scheduled-build loop with hour-long work units does not
//     (LongRunning).
//   - How expensive is each reconcile?  Cheap status updates can run
//     on a tighter bucket; long-running poudriere/jail provisioning
//     wants a slower bucket so a flapping resource doesn't fire repeated
//     expensive operations.
package limits

import (
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Profile bundles the controller-runtime knobs that together determine a
// reconciler's API-pressure posture.  Apply a Profile to a
// controller.Options inside SetupWithManager and the values flow through
// to the underlying workqueue.
type Profile struct {
	// MaxConcurrent caps the number of in-flight reconciles for one
	// controller.  Set to 1 for any reconciler that mutates host state
	// (package installs, jail provisioning) where serialization is a
	// correctness requirement.  Higher values only make sense for
	// purely informational reconcilers and we currently have none.
	MaxConcurrent int

	// BaseDelay is the initial exponential-backoff delay applied to a
	// reconcile request after the first error.  30 seconds is a deliberate
	// choice: short enough that a transient API blip recovers within one
	// minute, long enough that a persistent error (missing CRD, RBAC
	// gap) doesn't blast the API server with retries.
	BaseDelay time.Duration

	// MaxDelay caps the per-item exponential backoff.  5 minutes is
	// short enough that a recovered failure resumes promptly when the
	// underlying problem is resolved, while still sparing the API from
	// the multi-hour delays the controller-runtime default produces.
	MaxDelay time.Duration

	// BucketEvery is the token-bucket refill interval (1 token every
	// BucketEvery).  Zero disables the bucket — useful for reconcilers
	// that have a stronger external serialization mechanism (e.g.
	// ConfigSet drains bursts of Secret/ConfigMap changes and relies on
	// MaxConcurrent=1 to serialize).
	BucketEvery time.Duration

	// BucketBurst is the maximum tokens the bucket can hold.  Burst=1
	// means strictly-once-per-BucketEvery; larger values let short
	// flurries through.  We currently keep this at 1 for all profiles.
	BucketBurst int
}

// ApplyTo writes this profile's values onto the given controller.Options
// in place.  Use it inside SetupWithManager:
//
//	opts := controller.Options{}
//	limits.LongRunning.ApplyTo(&opts)
//	return ctrl.NewControllerManagedBy(mgr).
//	    For(&MyKind{}).
//	    WithOptions(opts).
//	    Complete(r)
//
// MaxConcurrent and the rate limiter are always set; any other fields
// already on opts (e.g. CacheSyncTimeout) are preserved.
func (p Profile) ApplyTo(opts *controller.Options) {
	opts.MaxConcurrentReconciles = p.MaxConcurrent
	opts.RateLimiter = p.RateLimiter()
}

// RateLimiter constructs a workqueue.TypedRateLimiter[reconcile.Request]
// from this profile's parameters.  Exposed for tests and for callers that
// need the limiter without the rest of controller.Options.
func (p Profile) RateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	exp := workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](p.BaseDelay, p.MaxDelay)
	if p.BucketEvery <= 0 {
		return exp
	}
	bucket := &workqueue.TypedBucketRateLimiter[reconcile.Request]{
		Limiter: rate.NewLimiter(rate.Every(p.BucketEvery), p.BucketBurst),
	}
	return workqueue.NewTypedMaxOfRateLimiter(exp, bucket)
}

// Default is the baseline profile every reconciler should use unless it
// has a documented reason to differ.  Single-item concurrency, 30s→5m
// exponential failure backoff, and a 1-token-per-10s steady-state bucket
// so a busy reconciler can't starve its siblings sharing the manager.
//
// Pick this for: ManagedNode-style time-scheduled reconcilers and most
// new controllers added in the future.
var Default = Profile{
	MaxConcurrent: 1,
	BaseDelay:     30 * time.Second,
	MaxDelay:      5 * time.Minute,
	BucketEvery:   10 * time.Second,
	BucketBurst:   1,
}

// LongRunning suits reconcilers whose successful path performs expensive,
// minutes-to-hours work (jail provisioning, poudriere bulk, freebsd-update
// runs).  The 30-second bucket means even a misbehaving spec change can't
// fire heavy operations more than twice a minute, which is plenty for any
// human-driven workflow and protects against status-update loops.
//
// Pick this for: Jail, Poudriere, and any future controller whose
// successful Reconcile path takes longer than a few seconds.
var LongRunning = Profile{
	MaxConcurrent: 1,
	BaseDelay:     30 * time.Second,
	MaxDelay:      5 * time.Minute,
	BucketEvery:   30 * time.Second,
	BucketBurst:   1,
}

// FastReact is for reconcilers that need to drain a fan-out of dependent
// resource events quickly (ConfigSet watches Secret + ConfigMap + the
// local ManagedNode and re-queues all matching ConfigSets on every
// change).  Skips the steady-state bucket because per-reconcile work is
// fast and serialised by MaxConcurrent=1; keeps the per-item exponential
// backoff so a flaky reconcile doesn't lock the controller into a tight
// retry loop.  Backoff cap is shorter (3 min) than Default because
// ConfigSet recovery should resume promptly once a missing
// Secret/ConfigMap appears.
//
// Pick this for: ConfigSet-style fan-in reconcilers responding to
// high-frequency dependent-resource events.
var FastReact = Profile{
	MaxConcurrent: 1,
	BaseDelay:     30 * time.Second,
	MaxDelay:      3 * time.Minute,
	BucketEvery:   0,
	BucketBurst:   0,
}
