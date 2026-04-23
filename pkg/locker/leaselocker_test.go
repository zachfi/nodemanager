package locker

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testNamespace = "test-ns"
	testName      = "test-lock"
	testID        = "locker-123"
	otherID       = "locker-456"
	leaseDuration = 15 * time.Second
)

var (
	testReq = types.NamespacedName{Namespace: testNamespace, Name: testName}
	ctx     = context.Background()
	logger  = slog.New(slog.NewTextHandler(os.Stderr, nil))
)

// mustInt32Ptr is a helper function to return a pointer to an int32.
func mustInt32Ptr(i int32) *int32 {
	return &i
}

// createLease creates a valid Lease object with specified holder and time.
func createLease(holderID string, renewTime time.Time, duration int32) *coordinationv1.Lease {
	renewMiroTime := metav1.NewMicroTime(renewTime)

	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testReq.Name,
			Namespace: testReq.Namespace,
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holderID,
			RenewTime:            &renewMiroTime,
			LeaseDurationSeconds: mustInt32Ptr(duration),
		},
	}
}

func TestLeaseLocker_Lock(t *testing.T) {
	// The time used for renewals in the tests
	now := time.Now()

	tests := []struct {
		name         string
		existingObjs []runtime.Object
		expectError  bool
		description  string
	}{
		{
			name:        "Acquire_New_Lock_Success",
			description: "Should successfully acquire a lock by creating a new Lease (IsAlreadyExists is not returned).",
			expectError: false,
		},
		{
			name:        "Acquire_Expired_Lock_Success",
			description: "Should successfully acquire an expired lock by updating it.",
			existingObjs: []runtime.Object{
				// Lease is held by 'otherID' and expired 1 minute ago.
				createLease(otherID, now.Add(-leaseDuration-time.Minute), int32(leaseDuration.Seconds())),
			},
			expectError: false,
		},
		{
			name:        "Lock_Already_Held_By_Me_Success",
			description: "Should treat a lock already held by the same instance as successfully acquired (no-op).",
			existingObjs: []runtime.Object{
				// Lease is held by testID and is not expired.
				createLease(testID, now, int32(leaseDuration.Seconds())),
			},
			expectError: false,
		},
		{
			name:        "Lock_Held_By_Others_Failure",
			description: "Should fail to acquire a lock held by another instance that is NOT expired.",
			existingObjs: []runtime.Object{
				// Lease is held by 'otherID' and renewed just now.
				createLease(otherID, now.Add(time.Minute), int32(leaseDuration.Seconds())),
			},
			expectError: true,
		},
	}

	cfg := Config{}
	cfg.RegisterFlagsAndApplyDefaults("", &flag.FlagSet{})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up the fake clientset with existing objects
			fakeClient := fake.NewSimpleClientset(tt.existingObjs...)

			// Initialize the leaseLocker with the fake client
			locker := NewLeaseLocker(ctx, logger, cfg, fakeClient, testReq.Namespace, testID)

			err := locker.Lock(ctx, testReq) // uses config default TTL

			if tt.expectError {
				if err == nil {
					t.Errorf("Lock() expected an error but got nil")
				}

				require.False(t, locker.Locked(ctx, testReq))
				// Skip further checks if error is expected and occurred
				return
			} else {
				require.True(t, locker.Locked(ctx, testReq))
			}

			if err != nil {
				t.Fatalf("Lock() unexpected error: %v", err)
			}

			// Verification after successful lock
			lease, getErr := fakeClient.CoordinationV1().Leases(testReq.Namespace).Get(ctx, testReq.Name, metav1.GetOptions{})
			if getErr != nil {
				t.Fatalf("Failed to get Lease after successful lock: %v", getErr)
			}

			if *lease.Spec.HolderIdentity != testID {
				t.Errorf("Lock not held by expected ID. Got: %s, Want: %s", *lease.Spec.HolderIdentity, testID)
			}
		})
	}
}

func TestLeaseLocker_Unlock(t *testing.T) {
	now := time.Now()

	// Create a Lease held by our test ID
	heldByMe := createLease(testID, now, int32(leaseDuration.Seconds()))
	// Create a Lease held by another ID
	heldByOthers := createLease(otherID, now, int32(leaseDuration.Seconds()))

	cfg := Config{}
	cfg.RegisterFlagsAndApplyDefaults("", &flag.FlagSet{})

	ctx := context.Background()

	namespace := "test"

	tests := []struct {
		name         string
		existingObjs []runtime.Object
		holderID     string
		expectError  bool
	}{
		{
			name:         "Unlock_Success",
			existingObjs: []runtime.Object{heldByMe},
			holderID:     testID,
			expectError:  false,
		},
		{
			name:         "Unlock_Not_Held_By_Me_NoOp",
			existingObjs: []runtime.Object{heldByOthers},
			holderID:     testID, // Locker 123 attempts to unlock a lease held by 456
			expectError:  false,
		},
		{
			name:         "Unlock_Lease_Not_Found_NoOp",
			existingObjs: []runtime.Object{},
			holderID:     testID,
			expectError:  false,
		},
		// Test conflict scenario (requires using a fake that injects a conflict)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset(tt.existingObjs...)

			// Initialize the leaseLocker with the fake client
			locker := NewLeaseLocker(ctx, logger, cfg, fakeClient, namespace, testID)

			err := locker.Unlock(ctx, testReq)

			if tt.expectError {
				if err == nil {
					t.Errorf("Unlock() expected an error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unlock() unexpected error: %v", err)
			}

			// Verification after unlock attempt
			lease, getErr := fakeClient.CoordinationV1().Leases(testReq.Namespace).Get(ctx, testReq.Name, metav1.GetOptions{})

			// If we expected the lease to be there, check its state
			if len(tt.existingObjs) > 0 {
				if getErr != nil {
					t.Fatalf("Expected to find Lease after unlock attempt: %v", getErr)
				}

				// The holder identity should be nil after a successful unlock
				if tt.name == "Unlock_Success" && lease.Spec.HolderIdentity != nil {
					t.Errorf("Unlock failed to clear holder identity. Got: %s, Want: nil", *lease.Spec.HolderIdentity)
				}

				// For no-op cases, the holder should remain the original holder
				if tt.name == "Unlock_Not_Held_By_Me_NoOp" && (lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != otherID) {
					t.Errorf("Lease state changed unexpectedly. Holder should still be %s, got: %v", otherID, lease.Spec.HolderIdentity)
				}
			}
		})
	}
}

func TestLeaseLocker_LockFor(t *testing.T) {
	// LockFor should write the provided duration, not the config default.
	customTTL := 25 * time.Hour
	fakeClient := fake.NewSimpleClientset()
	cfg := Config{}
	cfg.RegisterFlagsAndApplyDefaults("", &flag.FlagSet{})

	lkr := NewLeaseLocker(ctx, logger, cfg, fakeClient, testReq.Namespace, testID)
	require.NoError(t, lkr.LockFor(ctx, testReq, customTTL))

	lease, err := fakeClient.CoordinationV1().Leases(testReq.Namespace).Get(ctx, testReq.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(customTTL.Seconds()), *lease.Spec.LeaseDurationSeconds)
	require.Equal(t, testID, *lease.Spec.HolderIdentity)
}

// TestLockConflict specifically tests the update retry logic against an injected conflict.
// func TestLock_Conflict(t *testing.T) {
// 	// 1. Set up a Lease that is expired (so we attempt to claim it)
// 	now := time.Now()
// 	expiredLease := createLease(otherID, now.Add(-leaseDuration-time.Minute), int32(leaseDuration.Seconds()))
//
// 	// 2. Create a fake client, but this time, configure it to return an error on the first update.
// 	fakeClient := fake.NewSimpleClientset(expiredLease)
//
// 	cfg := LockerConfig{}
// 	cfg.RegisterFlagsAndApplyDefaults("", nil)
//
// 	ctx := context.Background()
//
// 	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
//
// 	namespace := "test"
//
// 	// Configure the client to return a Conflict error on the first two Update calls,
// 	// and succeed on the third (allowing the loop to pass).
// 	var conflictCount int
// 	fakeClient.PrependReactor("update", "leases", func(action runtime.Action) (handled bool, ret runtime.Object, err error) {
// 		conflictCount++
// 		if conflictCount < 3 {
// 			// Return a Conflict error
// 			return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, testName, fmt.Errorf("simulated conflict"))
// 		}
// 		// Return success on the third attempt
// 		updateAction := action.(metav1.UpdateAction)
// 		return true, updateAction.GetObject(), nil
// 	})
//
// 	locker := NewLeaseLocker(ctx, logger, cfg, fakeClient, namespace, testID)
//
// 	err := locker.Lock(ctx, testReq)
// 	if err != nil {
// 		t.Fatalf("Lock() failed unexpectedly on retry: %v", err)
// 	}
//
// 	// Verify acquisition succeeded and that the holder is now us
// 	lease, _ := fakeClient.CoordinationV1().Leases(testReq.Namespace).Get(ctx, testReq.Name, metav1.GetOptions{})
// 	if *lease.Spec.HolderIdentity != testID {
// 		t.Errorf("Lock not acquired after conflict retries. Holder: %s", *lease.Spec.HolderIdentity)
// 	}
// 	if conflictCount != 3 {
// 		t.Errorf("Expected 3 update attempts (2 conflicts + 1 success), got %d", conflictCount)
// 	}
// }
