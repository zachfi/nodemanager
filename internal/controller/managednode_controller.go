/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	backoff "github.com/cenkalti/backoff/v4"
	"github.com/gorhill/cronexpr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonv1 "github.com/zachfi/nodemanager/api/v1"
	"github.com/zachfi/nodemanager/pkg/common"
	"github.com/zachfi/nodemanager/pkg/system"
)

// ManagedNodeReconciler reconciles a ManagedNode object
type ManagedNodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	tracer trace.Tracer
	logger *slog.Logger
}

//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes/finalizers,verbs=update

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *ManagedNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		err  error
		next time.Time
		node *commonv1.ManagedNode
	)
	logger := log.FromContext(ctx)

	attributes := []attribute.KeyValue{
		attribute.String("req", req.String()),
		attribute.String("namespace", req.Namespace),
	}

	ctx, span := r.tracer.Start(ctx, "Reconcile", trace.WithAttributes(attributes...))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	logger.Info("msg", "namespace", req.Namespace, "namespacedName", req.NamespacedName)
	node, err = r.getNode(ctx, req)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if node == nil {
		return ctrl.Result{}, nil
	}

	err = r.updateNodeLabels(ctx, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.updateNodeStatus(ctx, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	next, err = r.handleUpgrade(ctx, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !next.IsZero() {
		return ctrl.Result{RequeueAfter: time.Until(next)}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1.ManagedNode{}).
		Complete(r)
}

func (r *ManagedNodeReconciler) WithTracer(tracer trace.Tracer) {
	r.tracer = tracer
}

func (r *ManagedNodeReconciler) WithLogger(logger *slog.Logger) {
	r.logger = logger
}

func (r *ManagedNodeReconciler) getNode(ctx context.Context, req ctrl.Request) (*commonv1.ManagedNode, error) {
	var (
		err  error
		node commonv1.ManagedNode
	)

	hostname, err := os.Hostname()
	if err != nil {
		return nil, client.IgnoreNotFound(err)
	}

	if hostname != req.Name {
		return nil, nil
	}

	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return nil, client.IgnoreNotFound(err)
	}

	return &node, nil
}

func (r *ManagedNodeReconciler) updateNodeLabels(ctx context.Context, node *commonv1.ManagedNode) error {
	labels := common.DefaultLabels()

	nodeLabels := node.GetLabels()
	if nodeLabels == nil {
		nodeLabels = make(map[string]string)
	}

	var updateLabels bool
	for k, v := range labels {
		if vv, ok := node.Labels[k]; ok {
			if vv != v {
				updateLabels = true
				nodeLabels[k] = v
			}
		} else {
			updateLabels = true
			nodeLabels[k] = v
		}
	}

	if updateLabels {
		node.SetLabels(nodeLabels)
		r.logger.Info("updating labels", "labels", nodeLabels)

		f := func() error {
			return r.Update(ctx, node)
		}

		if err := retry.RetryOnConflict(retry.DefaultBackoff, f); err != nil {
			return fmt.Errorf("failed to update ManagedNode: %w", err)
		}
	}

	return nil
}

func (r *ManagedNodeReconciler) updateNodeStatus(ctx context.Context, node *commonv1.ManagedNode) error {
	node.Status = nodeStatus()
	r.logger.Info("updating node status", "node", node.Name, "release", node.Status.Release)

	f := func() error {
		return r.Status().Update(ctx, node)
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, f); err != nil {
		return fmt.Errorf("failed to update ManagedNode status: %w", err)
	}

	return nil
}

func (r *ManagedNodeReconciler) handleUpgrade(ctx context.Context, node *commonv1.ManagedNode) (time.Time, error) {
	var (
		err  error
		next time.Time
	)

	// Check if we are a node that performs upgrades
	if node.Spec.Upgrade.Schedule == "" {
		r.logger.Info("managed node has no upgrade schedule")
		return time.Time{}, nil
	}

	if node.Spec.Upgrade.Delay == "" {
		r.logger.Info("managed node has no upgrade delay")
		return time.Time{}, nil
	}

	// If we already have the upgrade lock, we assume that we have just upgraded.
	if r.hasUpgradeLock(node) {
		defer r.upgradeUnlock(ctx, node)

		node.Annotations[common.AnnotationLastUpgrade] = time.Now().Format(time.RFC3339)

		f := func() error {
			return r.Update(ctx, node)
		}

		if err = retry.RetryOnConflict(retry.DefaultBackoff, f); err != nil {
			return time.Time{}, fmt.Errorf("failed to set last upgrade time: %w", err)
		}

		return time.Time{}, nil
	}

	cron, err := cronexpr.Parse(node.Spec.Upgrade.Schedule)
	if err != nil {
		return next, fmt.Errorf("failed to parse schedule as cron expression: %w", err)
	}
	next = cron.Next(time.Now())
	if time.Until(next) > time.Minute {
		return next, nil
	} else {
		time.Sleep(time.Until(next))
	}

	lastUpgrade, err := time.Parse(time.RFC3339, node.Annotations[common.AnnotationLastUpgrade])
	if err != nil {
		r.logger.Error("failed to parse LastUpgrade annotation", "err", err)
	}

	delay, err := time.ParseDuration(node.Spec.Upgrade.Delay)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse upgrade delay: %w", err)
	}

	if time.Since(lastUpgrade) < delay {
		return lastUpgrade.Add(delay), nil
	}

	err = r.upgradeLock(ctx, node)
	if err != nil {
		return time.Time{}, err
	}

	systemHandler, err := system.GetSystemHandler(ctx, r.tracer, r.logger, &common.UnameInfoResolver{})
	if err != nil {
		return time.Time{}, err
	}

	err = systemHandler.Upgrade(ctx)
	if err != nil {
		return time.Time{}, err
	}

	systemHandler.Reboot(ctx)

	return time.Time{}, err
}

func (r *ManagedNodeReconciler) upgradeLock(ctx context.Context, node *commonv1.ManagedNode) error {
	ticker := backoff.NewTicker(backoff.NewExponentialBackOff())

	defer ticker.Stop()

out:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			nodes, err := r.nodesCurrentlyUpgrading(ctx, node.Spec.Upgrade.Group)
			if err != nil {
				return err
			}

			switch len(nodes) {
			case 0:
				break out
			case 1:
				if nodes[0] == node.Name {
					// We already have the lock
					break out
				}
			default:
				continue
			}
		}
	}

	// Set the annotation
	node.Annotations[common.AnnotationUpgrading] = time.Now().Format(time.RFC3339)
	err := r.Update(ctx, node)
	if err != nil {
		return fmt.Errorf("failed to acquire lock")
	}

	return nil
}

func (r *ManagedNodeReconciler) upgradeUnlock(ctx context.Context, node *commonv1.ManagedNode) {
	nodeAnnotations := node.GetAnnotations()
	delete(nodeAnnotations, common.AnnotationUpgrading)
	err := r.Update(ctx, node)
	if err != nil {
		r.logger.Error("failed removing upgrade annotation", "err", err)
		// Try again
		err = r.Update(ctx, node)
		if err != nil {
			r.logger.Error("retry failed removing upgrade annotation", "err", err)
		}
	}
}

func (r *ManagedNodeReconciler) hasUpgradeLock(node *commonv1.ManagedNode) bool {
	if _, ok := node.GetAnnotations()[common.AnnotationUpgrading]; ok {
		return true
	}

	return false
}

func (r *ManagedNodeReconciler) nodesCurrentlyUpgrading(ctx context.Context, group string) ([]string, error) {
	nodes := &commonv1.ManagedNodeList{}
	err := r.List(ctx, nodes)
	if err != nil {
		return nil, err
	}

	items := []string{}

	for _, n := range nodes.Items {
		if _, ok := n.GetAnnotations()[common.AnnotationUpgrading]; ok {
			if group != n.Spec.Upgrade.Group {
				continue
			}

			items = append(items, n.Name)
		}
	}

	return items, nil
}

func nodeStatus() commonv1.ManagedNodeStatus {
	var status commonv1.ManagedNodeStatus
	resolver := &common.UnameInfoResolver{}
	info := resolver.Info()
	status.Release = info.OS.Release
	return status
}
