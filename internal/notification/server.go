package notification

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	notificationv1 "github.com/zachfi/nodemanager/pkg/notification/v1"
)

// Server implements the NodeNotificationService gRPC service and the
// controller-runtime Runnable interface so it can be added to a manager.
type Server struct {
	notificationv1.UnimplementedNodeNotificationServiceServer

	cfg    Config
	logger *slog.Logger

	mu          sync.RWMutex
	subscribers map[string]chan *notificationv1.Event

	// approvals receives approval responses keyed by event ID.
	approvalsMu sync.Mutex
	approvals   map[string]chan *notificationv1.ApprovalResponse
}

// NewServer creates a new notification Server.
func NewServer(logger *slog.Logger, cfg Config) *Server {
	return &Server{
		cfg:         cfg,
		logger:      logger.With("component", "notification-server"),
		subscribers: make(map[string]chan *notificationv1.Event),
		approvals:   make(map[string]chan *notificationv1.ApprovalResponse),
	}
}

// Start implements manager.Runnable. It listens on a Unix domain socket and
// serves gRPC until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	// Remove stale socket file if it exists.
	if err := os.Remove(s.cfg.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale socket: %w", err)
	}

	lis, err := net.Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.SocketPath, err)
	}

	// Allow group read/write so the user-space agent can connect.
	if err := os.Chmod(s.cfg.SocketPath, 0660); err != nil {
		lis.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	srv := grpc.NewServer()
	notificationv1.RegisterNodeNotificationServiceServer(srv, s)

	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down notification server")
		srv.GracefulStop()
	}()

	s.logger.Info("notification server listening", "socket", s.cfg.SocketPath)
	if err := srv.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

// NeedLeaderElection returns false — the notification server runs on every node.
func (s *Server) NeedLeaderElection() bool {
	return false
}

// Subscribe implements the server-streaming RPC. It registers the caller as a
// subscriber and sends events until the stream context is cancelled.
func (s *Server) Subscribe(req *notificationv1.SubscribeRequest, stream notificationv1.NodeNotificationService_SubscribeServer) error {
	id := req.GetSessionId()
	if id == "" {
		id = uuid.NewString()
	}

	ch := make(chan *notificationv1.Event, 64)

	s.mu.Lock()
	s.subscribers[id] = ch
	s.mu.Unlock()

	s.logger.Info("subscriber connected", "user", req.GetUser(), "session", id)

	defer func() {
		s.mu.Lock()
		delete(s.subscribers, id)
		s.mu.Unlock()
		s.logger.Info("subscriber disconnected", "session", id)
	}()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case event, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

// RespondToApproval implements the unary RPC. It delivers the user's approval
// decision to the waiting upgrade handler.
func (s *Server) RespondToApproval(_ context.Context, req *notificationv1.ApprovalResponse) (*notificationv1.ApprovalResponseAck, error) {
	s.approvalsMu.Lock()
	ch, ok := s.approvals[req.GetEventId()]
	s.approvalsMu.Unlock()

	if !ok {
		return &notificationv1.ApprovalResponseAck{
			Accepted: false,
			Reason:   "no pending approval for this event ID",
		}, nil
	}

	select {
	case ch <- req:
		return &notificationv1.ApprovalResponseAck{Accepted: true}, nil
	default:
		return &notificationv1.ApprovalResponseAck{
			Accepted: false,
			Reason:   "approval already received",
		}, nil
	}
}

// Notify broadcasts an event to all connected subscribers.
func (s *Server) Notify(event *notificationv1.Event) {
	if event.GetId() == "" {
		event.Id = uuid.NewString()
	}
	if event.GetTimestamp() == nil {
		event.Timestamp = timestamppb.Now()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for id, ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			s.logger.Warn("dropping event for slow subscriber", "session", id, "event", event.GetId())
		}
	}
}

// WaitForApproval registers a pending approval request and returns a channel
// that will receive the user's response. The caller is responsible for
// cleaning up via CancelApproval if the deadline expires.
func (s *Server) WaitForApproval(eventID string) <-chan *notificationv1.ApprovalResponse {
	ch := make(chan *notificationv1.ApprovalResponse, 1)

	s.approvalsMu.Lock()
	s.approvals[eventID] = ch
	s.approvalsMu.Unlock()

	return ch
}

// CancelApproval removes a pending approval request.
func (s *Server) CancelApproval(eventID string) {
	s.approvalsMu.Lock()
	delete(s.approvals, eventID)
	s.approvalsMu.Unlock()
}

// HasSubscribers returns true if at least one agent is connected.
func (s *Server) HasSubscribers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers) > 0
}
