/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package watchdog

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
)

// ManagedNodeProbe returns a watchdog connectivity probe that does a direct,
// uncached read of this agent's own ManagedNode. Pass an APIReader
// (mgr.GetAPIReader()), NOT the cached client — the whole point is to hit the
// API server so the probe fails on a real connection break instead of
// succeeding against the informer cache.
//
// A NotFound response counts as success: the API server answered, which is
// exactly the connectivity the probe is meant to confirm. Treating NotFound as
// failure would falsely exit a healthy agent whenever its ManagedNode has not
// been created yet (startup race — the ManagedNode controller creates its own
// node's object) or was deleted out from under it.
func ManagedNodeProbe(reader client.Reader, name, namespace string) func(context.Context) error {
	return func(ctx context.Context) error {
		var mn commonv1.ManagedNode
		err := reader.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &mn)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
}
