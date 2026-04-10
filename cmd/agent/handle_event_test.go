package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zachfi/nodemanager/internal/notification"
	notificationv1 "github.com/zachfi/nodemanager/pkg/notification/v1"
)

// mockDesktop records calls to notify and notifyWithActions for assertions.
type mockDesktop struct {
	mu            sync.Mutex
	notifications []mockNotification
	actionCalls   []mockActionCall
}

type mockNotification struct {
	summary, body, icon string
}

type mockActionCall struct {
	summary, body, icon string
	actions             []string
	timeout             int32
	cb                  func(string)
}

func (m *mockDesktop) notify(summary, body, icon string) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = append(m.notifications, mockNotification{summary, body, icon})
	return uint32(len(m.notifications)), nil
}

func (m *mockDesktop) notifyWithActions(summary, body, icon string, actions []string, timeout int32, cb func(string)) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actionCalls = append(m.actionCalls, mockActionCall{summary, body, icon, actions, timeout, cb})
	return uint32(len(m.actionCalls)), nil
}

func (m *mockDesktop) close() {}

func (m *mockDesktop) getNotifications() []mockNotification {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockNotification, len(m.notifications))
	copy(out, m.notifications)
	return out
}

func (m *mockDesktop) getActionCalls() []mockActionCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockActionCall, len(m.actionCalls))
	copy(out, m.actionCalls)
	return out
}

// startTestServer starts a notification gRPC server on a temp socket and
// returns the server, a connected client, and a cleanup function.
func startTestServer(t *testing.T) (*notification.Server, notificationv1.NodeNotificationServiceClient, func()) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "test.sock")
	cfg := notification.Config{Enabled: true, SocketPath: sock}
	srv := notification.NewServer(slog.Default(), cfg)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)

	conn, err := grpc.NewClient(
		"unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := notificationv1.NewNodeNotificationServiceClient(conn)

	cleanup := func() {
		conn.Close()
		cancel()
		<-errCh
	}
	return srv, client, cleanup
}

func TestHandleEvent_Notification(t *testing.T) {
	mock := &mockDesktop{}
	logger := slog.Default()

	event := &notificationv1.Event{
		Id:        "test-1",
		Timestamp: timestamppb.Now(),
		Payload: &notificationv1.Event_Notification{
			Notification: &notificationv1.Notification{
				Title:    "Backup done",
				Body:     "/home backed up",
				Severity: notificationv1.Severity_SEVERITY_INFO,
			},
		},
	}

	handleEvent(context.Background(), logger, mock, nil, "testuser", event)

	notes := mock.getNotifications()
	require.Len(t, notes, 1)
	require.Equal(t, "Backup done", notes[0].summary)
	require.Equal(t, "/home backed up", notes[0].body)
	require.Equal(t, "dialog-information", notes[0].icon)
}

func TestHandleEvent_NotificationSeverityIcons(t *testing.T) {
	tests := []struct {
		severity notificationv1.Severity
		icon     string
	}{
		{notificationv1.Severity_SEVERITY_INFO, "dialog-information"},
		{notificationv1.Severity_SEVERITY_WARNING, "dialog-warning"},
		{notificationv1.Severity_SEVERITY_ERROR, "dialog-error"},
	}

	for _, tt := range tests {
		t.Run(tt.severity.String(), func(t *testing.T) {
			mock := &mockDesktop{}
			event := &notificationv1.Event{
				Id:        "sev-test",
				Timestamp: timestamppb.Now(),
				Payload: &notificationv1.Event_Notification{
					Notification: &notificationv1.Notification{
						Title:    "Test",
						Severity: tt.severity,
					},
				},
			}
			handleEvent(context.Background(), slog.Default(), mock, nil, "u", event)
			require.Equal(t, tt.icon, mock.getNotifications()[0].icon)
		})
	}
}

func TestHandleEvent_UpgradeStarted(t *testing.T) {
	mock := &mockDesktop{}

	event := &notificationv1.Event{
		Id:        "upg-1",
		Timestamp: timestamppb.Now(),
		Payload: &notificationv1.Event_UpgradeStarted{
			UpgradeStarted: &notificationv1.UpgradeStarted{
				Description: "system upgrade on testhost",
			},
		},
	}

	handleEvent(context.Background(), slog.Default(), mock, nil, "testuser", event)

	notes := mock.getNotifications()
	require.Len(t, notes, 1)
	require.Equal(t, "Upgrade Started", notes[0].summary)
	require.Equal(t, "system upgrade on testhost", notes[0].body)
	require.Equal(t, "system-software-update", notes[0].icon)
}

func TestHandleEvent_UpgradeCompletedSuccess(t *testing.T) {
	mock := &mockDesktop{}

	event := &notificationv1.Event{
		Id:        "upg-done",
		Timestamp: timestamppb.Now(),
		Payload: &notificationv1.Event_UpgradeCompleted{
			UpgradeCompleted: &notificationv1.UpgradeCompleted{
				Success:       true,
				RebootPending: true,
			},
		},
	}

	handleEvent(context.Background(), slog.Default(), mock, nil, "testuser", event)

	notes := mock.getNotifications()
	require.Len(t, notes, 1)
	require.Equal(t, "Upgrade Completed", notes[0].summary)
	require.Contains(t, notes[0].body, "reboot")
	require.Equal(t, "system-reboot", notes[0].icon)
}

func TestHandleEvent_UpgradeCompletedFailure(t *testing.T) {
	mock := &mockDesktop{}

	event := &notificationv1.Event{
		Id:        "upg-fail",
		Timestamp: timestamppb.Now(),
		Payload: &notificationv1.Event_UpgradeCompleted{
			UpgradeCompleted: &notificationv1.UpgradeCompleted{
				Success: false,
				Error:   "pacman failed",
			},
		},
	}

	handleEvent(context.Background(), slog.Default(), mock, nil, "testuser", event)

	notes := mock.getNotifications()
	require.Len(t, notes, 1)
	require.Equal(t, "Upgrade Failed", notes[0].summary)
	require.Equal(t, "pacman failed", notes[0].body)
	require.Equal(t, "dialog-error", notes[0].icon)
}

func TestHandleEvent_UpgradeApproval(t *testing.T) {
	srv, client, cleanup := startTestServer(t)
	defer cleanup()

	mock := &mockDesktop{}
	deadline := time.Now().Add(30 * time.Second)

	event := &notificationv1.Event{
		Id:        "approve-1",
		Timestamp: timestamppb.Now(),
		Payload: &notificationv1.Event_UpgradeApprovalRequest{
			UpgradeApprovalRequest: &notificationv1.UpgradeApprovalRequest{
				Description:   "system upgrade on testhost",
				Schedule:      timestamppb.Now(),
				Deadline:      timestamppb.New(deadline),
				DefaultAction: notificationv1.ApprovalAction_APPROVAL_ACTION_APPROVE,
			},
		},
	}

	// Register the approval on the server side so RespondToApproval works.
	approvalCh := srv.WaitForApproval("approve-1")

	handleEvent(context.Background(), slog.Default(), mock, client, "testuser", event)

	// Verify the notification was shown with actions.
	calls := mock.getActionCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "Upgrade Approval Required", calls[0].summary)
	require.Contains(t, calls[0].actions, "approve")
	require.Contains(t, calls[0].actions, "deny")

	// Simulate user clicking "approve".
	calls[0].cb("approve")

	// The server should receive the approval.
	select {
	case resp := <-approvalCh:
		require.Equal(t, notificationv1.ApprovalAction_APPROVAL_ACTION_APPROVE, resp.GetAction())
		require.Equal(t, "testuser", resp.GetUser())
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval response on server")
	}
}

func TestHandleEvent_UpgradeApprovalDeny(t *testing.T) {
	srv, client, cleanup := startTestServer(t)
	defer cleanup()

	mock := &mockDesktop{}

	event := &notificationv1.Event{
		Id:        "deny-1",
		Timestamp: timestamppb.Now(),
		Payload: &notificationv1.Event_UpgradeApprovalRequest{
			UpgradeApprovalRequest: &notificationv1.UpgradeApprovalRequest{
				Description: "system upgrade",
				Schedule:    timestamppb.Now(),
				Deadline:    timestamppb.New(time.Now().Add(30 * time.Second)),
			},
		},
	}

	approvalCh := srv.WaitForApproval("deny-1")

	handleEvent(context.Background(), slog.Default(), mock, client, "testuser", event)

	calls := mock.getActionCalls()
	require.Len(t, calls, 1)

	// Simulate user clicking "deny".
	calls[0].cb("deny")

	select {
	case resp := <-approvalCh:
		require.Equal(t, notificationv1.ApprovalAction_APPROVAL_ACTION_DENY, resp.GetAction())
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for deny response on server")
	}
}
