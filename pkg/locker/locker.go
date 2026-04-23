package locker

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

type Locker interface {
	// Lock acquires the named lease using the configured default TTL.
	// Use this for short-lived locks where the caller releases immediately.
	Lock(ctx context.Context, req types.NamespacedName) error
	// LockFor acquires the named lease with an explicit TTL.  Use this for
	// long-lived locks such as upgrade-group coordination, where the TTL
	// should span the interval between scheduled occurrences so that at most
	// one group member acts per schedule slot.
	LockFor(ctx context.Context, req types.NamespacedName, duration time.Duration) error
	Unlock(ctx context.Context, req types.NamespacedName) error
	Locked(ctx context.Context, req types.NamespacedName) bool
}
