package notification

import notificationv1 "github.com/zachfi/nodemanager/pkg/notification/v1"

// Notifier is the interface used by controllers to send events and check
// subscriber status. A nil Notifier means notifications are disabled.
type Notifier interface {
	// Notify broadcasts an event to all connected subscribers.
	Notify(event *notificationv1.Event)

	// HasSubscribers returns true if at least one agent is connected.
	HasSubscribers() bool

	// WaitForApproval registers a pending approval and returns a channel that
	// receives the user's response.
	WaitForApproval(eventID string) <-chan *notificationv1.ApprovalResponse

	// CancelApproval removes a pending approval request.
	CancelApproval(eventID string)
}
