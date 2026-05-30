package common

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	notificationv1 "github.com/zachfi/nodemanager/pkg/notification/v1"
)

func approvalTestNode() *commonv1.ManagedNode {
	return &commonv1.ManagedNode{}
}

// TestRequestUpgradeApproval_DeadlineDelays verifies the fail-safe default:
// when a subscriber was prompted but never responds before the deadline, the
// controller does NOT auto-approve — it delays (returns false) so the node is
// not upgraded behind the user's back.
func TestRequestUpgradeApproval_DeadlineDelays(t *testing.T) {
	r := &ManagedNodeReconciler{
		logger:   slog.Default(),
		notifier: &mockNotifier{hasSubscribers: true},
		cfg:      ManagedNodeConfig{ForgivenessPeriod: 50 * time.Millisecond},
	}

	approved, err := r.requestUpgradeApproval(context.Background(), approvalTestNode(), time.Now())
	require.NoError(t, err)
	require.False(t, approved, "no response before deadline must not auto-approve")
}

// TestRequestUpgradeApproval_DeadlineSendsDelayDefault verifies the request
// advertises DELAY as its default action so the agent UI matches the
// controller's fail-safe behavior.
func TestRequestUpgradeApproval_DeadlineSendsDelayDefault(t *testing.T) {
	notifier := &mockNotifier{hasSubscribers: true}
	r := &ManagedNodeReconciler{
		logger:   slog.Default(),
		notifier: notifier,
		cfg:      ManagedNodeConfig{ForgivenessPeriod: 50 * time.Millisecond},
	}

	_, err := r.requestUpgradeApproval(context.Background(), approvalTestNode(), time.Now())
	require.NoError(t, err)
	require.Len(t, notifier.events, 1)
	req := notifier.events[0].GetUpgradeApprovalRequest()
	require.NotNil(t, req)
	require.Equal(t, notificationv1.ApprovalAction_APPROVAL_ACTION_DELAY, req.GetDefaultAction())
}

// TestRequestUpgradeApproval_DelayResponse verifies an explicit DELAY response
// (what a dismiss now maps to) results in the upgrade being skipped.
func TestRequestUpgradeApproval_DelayResponse(t *testing.T) {
	ch := make(chan *notificationv1.ApprovalResponse, 1)
	ch <- &notificationv1.ApprovalResponse{Action: notificationv1.ApprovalAction_APPROVAL_ACTION_DELAY}
	r := &ManagedNodeReconciler{
		logger:   slog.Default(),
		notifier: &mockNotifier{hasSubscribers: true, approvalCh: ch},
		cfg:      ManagedNodeConfig{ForgivenessPeriod: time.Minute},
	}

	approved, err := r.requestUpgradeApproval(context.Background(), approvalTestNode(), time.Now())
	require.NoError(t, err)
	require.False(t, approved)
}

// TestRequestUpgradeApproval_ApproveResponse keeps the happy path honest: an
// explicit APPROVE still upgrades.
func TestRequestUpgradeApproval_ApproveResponse(t *testing.T) {
	ch := make(chan *notificationv1.ApprovalResponse, 1)
	ch <- &notificationv1.ApprovalResponse{Action: notificationv1.ApprovalAction_APPROVAL_ACTION_APPROVE}
	r := &ManagedNodeReconciler{
		logger:   slog.Default(),
		notifier: &mockNotifier{hasSubscribers: true, approvalCh: ch},
		cfg:      ManagedNodeConfig{ForgivenessPeriod: time.Minute},
	}

	approved, err := r.requestUpgradeApproval(context.Background(), approvalTestNode(), time.Now())
	require.NoError(t, err)
	require.True(t, approved)
}
