package common

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/grafana/dskit/backoff"
	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Locker is an interface that defines the methods for lock handling using an annotation key, and an optional group string.
// The annotation is the key of the annotation used to lock.  The value is a timestamp the lock was acquired.
// The key is the label on the node which we use to group, and the value is the name of the group.
type Locker interface {
	Lock(ctx context.Context, req types.NamespacedName, key, value string) error
	Unlock(ctx context.Context, req types.NamespacedName) error
	HasLock(ctx context.Context, req types.NamespacedName) (bool, error)
}

type ReaderWriter interface {
	client.Reader
	client.Writer
}

var _ Locker = &keyLocker{}

type keyLocker struct {
	logger     *slog.Logger
	reader     client.Reader
	writer     client.Writer
	backoff    *backoff.Backoff
	annotation string
}

func NewKeyLocker(logger *slog.Logger, cfg LockerConfig, rw ReaderWriter, annotation string) Locker {
	b := backoff.New(context.Background(), cfg.Backoff)

	return &keyLocker{
		logger:     logger,
		reader:     rw,
		writer:     rw,
		backoff:    b,
		annotation: annotation,
	}
}

func (l *keyLocker) Lock(ctx context.Context, req types.NamespacedName, key, value string) error {
	defer l.backoff.Reset()

	lockCtx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()

	nodeList := &commonv1.ManagedNodeList{}
	var err error

	lockFreeCounter := 0
	lockFreeCounterThreshold := 3 // Threshold to allow lock acquisition without backoff

	l.logger.Info("attempting to acquire lock", "node", req.Name, "key", key, "value", value)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-lockCtx.Done():
			return lockCtx.Err()
		default:
			matcher := client.MatchingLabels{}
			if key != "" && value != "" {
				matcher = client.MatchingLabels{key: value}
			}

			err = l.reader.List(ctx, nodeList, matcher)
			if err != nil {
				l.logger.Error("failed to list nodes", "error", err, "waiting", l.backoff.NextDelay().String())
				l.backoff.Wait()
			}

			lockedNodes := 0
			for _, node := range nodeList.Items {
				if node.Annotations != nil && node.Annotations[l.annotation] != "" {
					lockedNodes++
					l.logger.Info("node has the lock", "node", node.Name, "annotation", node.Annotations[l.annotation])
				}
			}

			// TODO: if we encounter locked nodes, we should backoff.
			if lockedNodes > 0 {
				l.backoff.Wait()
				continue
			}

			if lockedNodes == 0 {
				// Ensure we are free to lock three times in a row
				lockFreeCounter++
				if lockFreeCounter < lockFreeCounterThreshold {
					l.logger.Info("no nodes have the lock, waiting before retrying", "waiting", l.backoff.NextDelay().String(), "attempt", lockFreeCounter)
					l.backoff.Wait()
					continue
				}
				l.logger.Debug("no nodes have the lock, proceeding to acquire lock", "node", req.Name)

				// Fetch our node to update it.
				node := &commonv1.ManagedNode{}
				err = l.reader.Get(ctx, req, node)
				if err != nil {
					return fmt.Errorf("failed to get node %s: %w", req.Name, err)
				}

				// No nodes have the lock, we can set the annotation
				if node.Annotations == nil {
					node.Annotations = make(map[string]string)
				}
				node.Annotations[l.annotation] = time.Now().Format(time.RFC3339)

				err = l.writer.Update(ctx, node)
				if err != nil {
					l.logger.Error("failed to acquire lock", "error", err)
					l.backoff.Wait()
					continue
				}

				l.logger.Info("lock acquired", "node", req.Name, "annotation", l.annotation)

				return nil // Lock acquired successfully
			} else {
				// Reset the lock-free counter since we found nodes with the lock
				lockFreeCounter = 0
			}

			if lockedNodes == 1 && len(nodeList.Items) == 1 && nodeList.Items[0].Name == req.Name {
				// We already have the lock, no need to do anything
				return nil
			}

		}
	}
}

func (l *keyLocker) Unlock(ctx context.Context, req types.NamespacedName) error {
	defer l.backoff.Reset()

	unlockCtx, cancel := context.WithTimeout(ctx, 1*time.Hour)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-unlockCtx.Done():
			return unlockCtx.Err()
		default:
			// Fetch the node to update it
			node := &commonv1.ManagedNode{}
			err := l.reader.Get(ctx, req, node)
			if err != nil {
				l.logger.Error("failed to get node for unlocking", "error", err)
				l.backoff.Wait()
				continue
			}

			// Remove the annotation
			if node.Annotations != nil {
				delete(node.Annotations, l.annotation)
			}

			err = l.writer.Update(ctx, node)
			if err != nil {
				l.logger.Error("failed to unlock node", "error", err)
				l.backoff.Wait()
				continue
			}

			l.logger.Info("lock released", "node", req.Name, "annotation", l.annotation)

			return nil // Unlock successful
		}
	}
}

func (l *keyLocker) HasLock(ctx context.Context, req types.NamespacedName) (bool, error) {
	defer l.backoff.Reset()

	hasLockCtx, cancel := context.WithTimeout(ctx, 1*time.Hour)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-hasLockCtx.Done():
			return false, hasLockCtx.Err()
		default:
			node := &commonv1.ManagedNode{}
			err := l.reader.Get(ctx, req, node)
			if err != nil {
				if client.IgnoreNotFound(err) != nil {
					l.logger.Error("failed to get node for lock check", "error", err)
				}
				l.backoff.Wait()
				continue
			}

			if node.Annotations != nil && node.Annotations[l.annotation] != "" {
				return true, nil // Lock exists
			}

			return false, nil // Lock does not exist
		}
	}
}
