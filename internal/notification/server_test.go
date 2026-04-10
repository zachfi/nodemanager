package notification

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notificationv1 "github.com/zachfi/nodemanager/pkg/notification/v1"
	"log/slog"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "test.sock")
	cfg := Config{Enabled: true, SocketPath: sock}
	logger := slog.Default()
	return NewServer(logger, cfg), sock
}

func TestSubscribeReceivesEvents(t *testing.T) {
	srv, sock := testServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in background.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Wait for socket to be ready.
	time.Sleep(50 * time.Millisecond)

	conn, err := grpc.NewClient(
		"unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := notificationv1.NewNodeNotificationServiceClient(conn)

	stream, err := client.Subscribe(ctx, &notificationv1.SubscribeRequest{
		User:      "testuser",
		SessionId: "test-session",
	})
	require.NoError(t, err)

	// Give the subscriber time to register.
	time.Sleep(50 * time.Millisecond)

	// Send an event through the server's Notify method.
	srv.Notify(&notificationv1.Event{
		Payload: &notificationv1.Event_UpgradeStarted{
			UpgradeStarted: &notificationv1.UpgradeStarted{
				Description: "test upgrade",
			},
		},
	})

	event, err := stream.Recv()
	require.NoError(t, err)
	require.NotEmpty(t, event.GetId())
	require.NotNil(t, event.GetTimestamp())

	started := event.GetUpgradeStarted()
	require.NotNil(t, started)
	require.Equal(t, "test upgrade", started.GetDescription())

	cancel()
	require.NoError(t, <-errCh)
}

func TestRespondToApproval(t *testing.T) {
	srv, sock := testServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)

	conn, err := grpc.NewClient(
		"unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := notificationv1.NewNodeNotificationServiceClient(conn)

	// Register a pending approval.
	approvalCh := srv.WaitForApproval("evt-1")

	// Send the response via gRPC.
	ack, err := client.RespondToApproval(ctx, &notificationv1.ApprovalResponse{
		EventId: "evt-1",
		Action:  notificationv1.ApprovalAction_APPROVAL_ACTION_APPROVE,
		User:    "testuser",
	})
	require.NoError(t, err)
	require.True(t, ack.GetAccepted())

	// The channel should have the response.
	select {
	case resp := <-approvalCh:
		require.Equal(t, notificationv1.ApprovalAction_APPROVAL_ACTION_APPROVE, resp.GetAction())
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval response")
	}

	// Unknown event ID should return not-accepted.
	ack, err = client.RespondToApproval(ctx, &notificationv1.ApprovalResponse{
		EventId: "unknown",
		Action:  notificationv1.ApprovalAction_APPROVAL_ACTION_DENY,
	})
	require.NoError(t, err)
	require.False(t, ack.GetAccepted())

	cancel()
	require.NoError(t, <-errCh)
}
