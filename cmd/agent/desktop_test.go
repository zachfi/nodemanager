package main

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDesktopHandleClose_DismissInvokesCallback verifies that an explicit
// user dismiss (reason 2) invokes the registered callback with the dismiss
// sentinel so the upstream handler can map it to a real response instead of
// silently dropping the request.
func TestDesktopHandleClose_DismissInvokesCallback(t *testing.T) {
	d := &desktop{logger: slog.Default(), actions: map[uint32]func(string){}}

	got := make(chan string, 1)
	d.actions[7] = func(k string) { got <- k }

	d.handleClose(7, closeReasonDismissed)

	select {
	case k := <-got:
		require.Equal(t, actionKeyDismiss, k)
	case <-time.After(time.Second):
		t.Fatal("callback was not invoked on dismiss")
	}

	d.actionMu.Lock()
	_, exists := d.actions[7]
	d.actionMu.Unlock()
	require.False(t, exists, "callback should be removed after dismiss")
}

// TestDesktopHandleClose_ExpiredDropsCallback verifies that a non-dismiss
// close reason (e.g. expiry) cleans up the callback without invoking it; the
// controller's deadline handling owns the no-response path.
func TestDesktopHandleClose_ExpiredDropsCallback(t *testing.T) {
	d := &desktop{logger: slog.Default(), actions: map[uint32]func(string){}}

	called := false
	d.actions[7] = func(k string) { called = true }

	d.handleClose(7, closeReasonExpired)

	require.False(t, called, "callback must not be invoked on expiry")

	d.actionMu.Lock()
	_, exists := d.actions[7]
	d.actionMu.Unlock()
	require.False(t, exists, "callback should be removed after close")
}

// TestDesktopHandleClose_UnknownIDNoPanic ensures closing an id with no
// registered callback is a harmless no-op.
func TestDesktopHandleClose_UnknownIDNoPanic(t *testing.T) {
	d := &desktop{logger: slog.Default(), actions: map[uint32]func(string){}}
	require.NotPanics(t, func() { d.handleClose(99, closeReasonDismissed) })
}
