package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const reconcileTriggerAnnotation = "nodemanager.io/reconcile-trigger"

// nodePatcher is the subset of client.Client used by sighupTrigger.
type nodePatcher interface {
	Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
	Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error
}

// sighupTrigger is a manager.Runnable that listens for SIGHUP and triggers
// reconciliation of the local ManagedNode by patching a timestamp annotation.
// The existing configSetsOnNodeChange watch maps the ManagedNode change to all
// matching ConfigSets, so both the ManagedNode and ConfigSet reconcilers are
// re-queued without any changes to the controllers themselves.
type sighupTrigger struct {
	client    nodePatcher
	logger    *slog.Logger
	hostname  string
	namespace string
	// ready is closed after signal.Notify returns, giving tests a deterministic
	// synchronisation point before sending SIGHUP. Leave nil in production.
	ready chan struct{}
}

func (s *sighupTrigger) Start(ctx context.Context) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)

	if s.ready != nil {
		close(s.ready)
	}

	for {
		select {
		case <-ch:
			s.logger.Info("SIGHUP received, triggering reconciliation")
			if err := s.trigger(ctx); err != nil {
				s.logger.Error("failed to trigger reconciliation via SIGHUP", "err", err)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *sighupTrigger) trigger(ctx context.Context) error {
	var node commonv1.ManagedNode
	if err := s.client.Get(ctx, types.NamespacedName{Name: s.hostname, Namespace: s.namespace}, &node); err != nil {
		return err
	}
	patch := client.MergeFrom(node.DeepCopy())
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[reconcileTriggerAnnotation] = time.Now().UTC().Format(time.RFC3339)
	return s.client.Patch(ctx, &node, patch)
}
