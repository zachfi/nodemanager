package main

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/godbus/dbus/v5"
)

// desktopNotifier is the interface used by the event handler to show
// desktop notifications. The concrete implementation uses D-Bus; tests
// provide a mock.
type desktopNotifier interface {
	notify(summary, body, icon string) (uint32, error)
	notifyWithActions(summary, body, icon string, actions []string, timeout int32, cb func(actionKey string)) (uint32, error)
	close()
}

const (
	dbusNotifyDest  = "org.freedesktop.Notifications"
	dbusNotifyPath  = "/org/freedesktop/Notifications"
	dbusNotifyIface = "org.freedesktop.Notifications"
)

// NotificationClosed reason codes from the freedesktop notification spec.
const (
	closeReasonExpired   uint32 = 1 // the notification expired
	closeReasonDismissed uint32 = 2 // the notification was dismissed by the user
)

// actionKeyDismiss is the sentinel action key passed to a notification's
// callback when the user explicitly dismisses it, so the upstream handler can
// map a dismiss to an explicit response instead of silently dropping it.
const actionKeyDismiss = "dismiss"

// desktop wraps the org.freedesktop.Notifications D-Bus interface.
type desktop struct {
	conn   *dbus.Conn
	logger *slog.Logger

	// actionMu guards the pending action callbacks.
	actionMu sync.Mutex
	actions  map[uint32]func(actionKey string)
}

// newDesktop connects to the session bus and starts listening for
// ActionInvoked signals.
func newDesktop(logger *slog.Logger) (*desktop, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect session bus: %w", err)
	}

	d := &desktop{
		conn:    conn,
		logger:  logger,
		actions: make(map[uint32]func(string)),
	}

	// Subscribe to ActionInvoked signals so we can handle user clicks on
	// notification action buttons.
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(dbusNotifyPath),
		dbus.WithMatchInterface(dbusNotifyIface),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		conn.Close()
		return nil, fmt.Errorf("add match signal: %w", err)
	}

	// Also listen for NotificationClosed so we can clean up callbacks.
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(dbusNotifyPath),
		dbus.WithMatchInterface(dbusNotifyIface),
		dbus.WithMatchMember("NotificationClosed"),
	); err != nil {
		conn.Close()
		return nil, fmt.Errorf("add match signal: %w", err)
	}

	go d.listenSignals()

	return d, nil
}

// listenSignals processes D-Bus signals for action invocations and
// notification closures.
func (d *desktop) listenSignals() {
	ch := make(chan *dbus.Signal, 16)
	d.conn.Signal(ch)

	for sig := range ch {
		switch sig.Name {
		case dbusNotifyIface + ".ActionInvoked":
			if len(sig.Body) < 2 {
				continue
			}
			id, ok1 := sig.Body[0].(uint32)
			key, ok2 := sig.Body[1].(string)
			if !ok1 || !ok2 {
				continue
			}
			d.actionMu.Lock()
			if cb, exists := d.actions[id]; exists {
				delete(d.actions, id)
				go cb(key)
			}
			d.actionMu.Unlock()

		case dbusNotifyIface + ".NotificationClosed":
			if len(sig.Body) < 1 {
				continue
			}
			id, ok := sig.Body[0].(uint32)
			if !ok {
				continue
			}
			var reason uint32
			if len(sig.Body) >= 2 {
				reason, _ = sig.Body[1].(uint32)
			}
			d.handleClose(id, reason)
		}
	}
}

// handleClose processes a NotificationClosed signal. When the user explicitly
// dismissed the notification (reason 2), the registered callback is invoked
// with actionKeyDismiss so the upstream handler can map it to an explicit
// response; for all other reasons (expiry, programmatic close) the callback is
// dropped and the controller's deadline handling owns the no-response path.
// The callback is always removed from the pending set.
func (d *desktop) handleClose(id, reason uint32) {
	d.actionMu.Lock()
	cb, exists := d.actions[id]
	if exists {
		delete(d.actions, id)
	}
	d.actionMu.Unlock()

	if exists && reason == closeReasonDismissed {
		go cb(actionKeyDismiss)
	}
}

// notify shows a simple desktop notification. Returns the notification ID.
func (d *desktop) notify(summary, body, icon string) (uint32, error) {
	obj := d.conn.Object(dbusNotifyDest, dbusNotifyPath)
	call := obj.Call(dbusNotifyIface+".Notify", 0,
		"nodemanager",             // app_name
		uint32(0),                 // replaces_id
		icon,                      // app_icon
		summary,                   // summary
		body,                      // body
		[]string{},                // actions
		map[string]dbus.Variant{}, // hints
		int32(-1),                 // expire_timeout (-1 = server default)
	)
	if call.Err != nil {
		return 0, call.Err
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// notifyWithActions shows a notification with clickable action buttons.
// actions is a flat list of [key, label, key, label, ...] pairs per the
// org.freedesktop.Notifications spec. The callback receives the key of the
// action the user clicked.
func (d *desktop) notifyWithActions(summary, body, icon string, actions []string, timeout int32, cb func(actionKey string)) (uint32, error) {
	obj := d.conn.Object(dbusNotifyDest, dbusNotifyPath)
	call := obj.Call(dbusNotifyIface+".Notify", 0,
		"nodemanager",
		uint32(0),
		icon,
		summary,
		body,
		actions,
		map[string]dbus.Variant{
			"urgency": dbus.MakeVariant(byte(2)), // critical — don't auto-dismiss
		},
		timeout,
	)
	if call.Err != nil {
		return 0, call.Err
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		return 0, err
	}

	d.actionMu.Lock()
	d.actions[id] = cb
	d.actionMu.Unlock()

	return id, nil
}

// close closes the D-Bus connection.
func (d *desktop) close() {
	d.conn.Close()
}
