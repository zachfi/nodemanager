package locker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/grafana/dskit/backoff"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

var _ Locker = &leaseLocker{}

type leaseLocker struct {
	logger    *slog.Logger
	clientset kubernetes.Interface
	namespace string
	id        string

	cfg Config

	mtx sync.Mutex
}

func NewLeaseLocker(ctx context.Context, logger *slog.Logger, cfg Config, clientset kubernetes.Interface, namespace, id string) Locker {
	return &leaseLocker{
		logger:    logger,
		cfg:       cfg,
		clientset: clientset,
		namespace: namespace,
		id:        id,
	}
}

func (l *leaseLocker) Lock(ctx context.Context, req types.NamespacedName) error {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	var (
		b                    = backoff.New(ctx, l.cfg.Backoff)
		leaseInterface       = l.clientset.CoordinationV1().Leases(req.Namespace)
		currentMicroTime     = metav1.NewMicroTime(time.Now())
		leaseDurationSeconds = int32(l.cfg.LeaseDuration.Seconds())
		lockData             = coordinationv1.LeaseSpec{
			HolderIdentity:       &l.id,
			LeaseDurationSeconds: &leaseDurationSeconds,
			AcquireTime:          &currentMicroTime,
			RenewTime:            &currentMicroTime,
		}
		newLease = &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: req.Namespace},
			Spec:       lockData,
		}
	)

	// Attempt to create the lease
	_, err := leaseInterface.Create(ctx, newLease, metav1.CreateOptions{})
	if err == nil {
		l.logger.Info("lock acquired", "lease", req.String(), "id", l.id)
		return nil
	}

	// If Create failed and it's NOT 'AlreadyExists', return the error
	if !apierrors.IsAlreadyExists(err) {
		return err
	}

	for b.Ongoing() {
		existingLease, getErr := leaseInterface.Get(ctx, req.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}

		var (
			isExpired  = existingLease.Spec.RenewTime.Add(time.Duration(*existingLease.Spec.LeaseDurationSeconds) * time.Second).Before(time.Now())
			isHeldByMe = existingLease.Spec.HolderIdentity != nil && *existingLease.Spec.HolderIdentity == l.id
		)

		if isHeldByMe {
			l.logger.Info("lock already held by this instance (treating as acquired)", "lease", req.String())
			return nil // Already holding the lock
		}

		if !isExpired {
			// Lock is held and not expired
			return apierrors.NewConflict(coordinationv1.Resource("leases"), req.Name, fmt.Errorf("lock held by another instance and not expired"))
		}

		// Replace the spec with our lock
		existingLease.Spec = lockData

		_, updateErr := leaseInterface.Update(ctx, existingLease, metav1.UpdateOptions{})
		if updateErr == nil {
			l.logger.Info("lock acquired by updating expired Lease", "lease", req.String())
			return nil // Success!
		}

		if !apierrors.IsConflict(updateErr) {
			return updateErr
		}

		// Conflict occurred; wait and retry
		b.Wait()
	}

	// If the loop completes, it means we failed all retry attempts due to conflict
	return apierrors.NewConflict(coordinationv1.Resource("leases"), req.Name, fmt.Errorf("failed to acquire lock after multiple retries due to contention"))
}

func (l *leaseLocker) Unlock(ctx context.Context, req types.NamespacedName) error {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	var (
		b              = backoff.New(ctx, l.cfg.Backoff)
		leaseInterface = l.clientset.CoordinationV1().Leases(req.Namespace)
	)

	for b.Ongoing() {

		existingLease, err := leaseInterface.Get(ctx, req.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				l.logger.Info("Unlock called but Lease not found, assuming released", "lease", req.String())
				return nil
			}
			return err
		}

		if existingLease.Spec.HolderIdentity == nil || *existingLease.Spec.HolderIdentity != l.id {
			// **Maintainability**: Log a warning, but return nil as we can't lock what we don't own,
			// and this isn't a critical error that should stop the node's work.
			l.logger.Info("Unlock attempted on Lease not held by this identity", "lease", req.String(), "currentHolder", existingLease.Spec.HolderIdentity)
			return nil
		}

		// C. Prepare to release ownership and explicitly expire the Lease
		pastMicroTime := metav1.NewMicroTime(time.Now().Add(-2 * time.Hour))

		existingLease.Spec.HolderIdentity = nil // Key to release the lock
		existingLease.Spec.RenewTime = &pastMicroTime
		// Retain ResourceVersion for Optimistic Locking

		// D. Attempt the Update
		_, updateErr := leaseInterface.Update(ctx, existingLease, metav1.UpdateOptions{})
		if updateErr == nil {
			l.logger.Info("lock released", "lease", req.String(), "id", l.id)
			return nil // Success!
		}

		// E. Handle Conflicts: Retry if ResourceVersion mismatch
		if !apierrors.IsConflict(updateErr) {
			return updateErr // Return non-conflict error immediately
		}

		// Conflict occurred; wait and retry
		b.Wait()
	}

	// If the loop completes, it means we failed all retry attempts due to conflict
	return apierrors.NewConflict(coordinationv1.Resource("leases"), req.Name, fmt.Errorf("failed to release lock after multiple retries due to contention"))
}

func (l *leaseLocker) Locked(ctx context.Context, req types.NamespacedName) bool {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	leaseInterface := l.clientset.CoordinationV1().Leases(req.Namespace)

	existingLease, err := leaseInterface.Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		return false
	}

	if existingLease.Spec.HolderIdentity != nil && *existingLease.Spec.HolderIdentity == l.id {
		return true
	}

	return false
}
