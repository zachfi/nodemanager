package main

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeNodePatcher implements nodePatcher for tests.
type fakeNodePatcher struct {
	mu   sync.Mutex
	node commonv1.ManagedNode
}

func (f *fakeNodePatcher) Get(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	*obj.(*commonv1.ManagedNode) = *f.node.DeepCopy()
	return nil
}

func (f *fakeNodePatcher) Patch(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.node = *obj.(*commonv1.ManagedNode).DeepCopy()
	return nil
}

func (f *fakeNodePatcher) annotations() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.node.Annotations
}

func newFakePatcher() *fakeNodePatcher {
	return &fakeNodePatcher{
		node: commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Namespace: "default"},
		},
	}
}

func TestSighupTrigger_Trigger(t *testing.T) {
	fp := newFakePatcher()
	trig := &sighupTrigger{
		client:    fp,
		logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
		hostname:  "test-node",
		namespace: "default",
	}

	require.NoError(t, trig.trigger(context.Background()))

	ann := fp.annotations()
	require.Contains(t, ann, reconcileTriggerAnnotation)

	ts, err := time.Parse(time.RFC3339, ann[reconcileTriggerAnnotation])
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().UTC(), ts, 5*time.Second)
}

func TestSighupTrigger_Trigger_PreservesExistingAnnotations(t *testing.T) {
	fp := newFakePatcher()
	fp.node.Annotations = map[string]string{"existing-key": "existing-value"}

	trig := &sighupTrigger{
		client:    fp,
		logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
		hostname:  "test-node",
		namespace: "default",
	}

	require.NoError(t, trig.trigger(context.Background()))

	ann := fp.annotations()
	require.Equal(t, "existing-value", ann["existing-key"], "existing annotations must be preserved")
	require.Contains(t, ann, reconcileTriggerAnnotation)
}

func TestSighupTrigger_Start_RespondsToSIGHUP(t *testing.T) {
	fp := newFakePatcher()
	ready := make(chan struct{})
	trig := &sighupTrigger{
		client:    fp,
		logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
		hostname:  "test-node",
		namespace: "default",
		ready:     ready,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = trig.Start(ctx)
	}()

	<-ready // wait until signal.Notify has been called

	p, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, p.Signal(syscall.SIGHUP))

	require.Eventually(t, func() bool {
		_, ok := fp.annotations()[reconcileTriggerAnnotation]
		return ok
	}, 2*time.Second, 20*time.Millisecond, "annotation must appear after SIGHUP")

	cancel()
	<-done
}
