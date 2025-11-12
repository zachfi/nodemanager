package locker

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
)

type Locker interface {
	Lock(ctx context.Context, req types.NamespacedName) error
	Unlock(ctx context.Context, req types.NamespacedName) error
	Locked(ctx context.Context, req types.NamespacedName) bool
}
