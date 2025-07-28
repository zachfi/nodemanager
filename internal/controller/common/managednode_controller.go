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

package common

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gorhill/cronexpr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/common"
	"github.com/zachfi/nodemanager/pkg/common/labels"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/util"
)

// ManagedNodeReconciler reconciles a ManagedNode object
type ManagedNodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	tracer trace.Tracer
	logger *slog.Logger
	system handler.System
	locker Locker
}

//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to keep the k8s resource in sync with the current state of the node.
func (r *ManagedNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		err  error
		next time.Time
		node *commonv1.ManagedNode
	)

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

	node, err = util.GetNode(ctx, r, req, r.system.Node())
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if node == nil {
		r.logger.Info("node not found, skipping reconciliation", "req", req.NamespacedName)
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

func (r *ManagedNodeReconciler) WithSystem(system handler.System) {
	r.system = system
}

func (r *ManagedNodeReconciler) WithLocker(locker Locker) {
	r.locker = locker
}

func (r *ManagedNodeReconciler) updateNodeLabels(ctx context.Context, node *commonv1.ManagedNode) error {
	labels := labels.DefaultLabels(ctx, r.system.Node())

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
	info := r.system.Node().Info(ctx)
	node.Status.Release = info.OS.Release

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
	var err error

	// Check if we are a node that performs upgrades
	if node.Spec.Upgrade.Schedule == "" {
		r.logger.Info("managed node has no upgrade schedule")
		return time.Time{}, nil
	}

	if node.Spec.Upgrade.Delay == "" {
		r.logger.Info("managed node has no upgrade delay")
		return time.Time{}, nil
	}

	req := types.NamespacedName{
		Name:      node.Name,
		Namespace: node.Namespace,
	}

	locked, err := r.locker.HasLock(ctx, req)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to check for upgrade lock: %w", err)
	}

	// If we already have the upgrade lock, we assume that we have just upgraded.
	if locked {
		defer r.locker.Unlock(ctx, req)

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
		return time.Time{}, fmt.Errorf("failed to parse schedule as cron expression: %w", err)
	}

	next := cron.Next(time.Now())

	r.logger.Info("next upgrade time", "schedule", node.Spec.Upgrade.Schedule, "until", time.Until(next))

	if time.Since(next) < 1*time.Minute {
		// If the next upgrade time is less than a minute in teh past, we execute immediately.
	} else if time.Until(next) < 1*time.Minute {
		// The the upgrade time is less than a minute in the future, we wait and then continue to execute.
		time.Sleep(time.Until(next))
	} else {
		// If we are outside of the minute range in either direction, return the time which we should check again.
		return next, nil
	}

	var lastUpgrade time.Time
	if last, ok := node.Annotations[common.AnnotationLastUpgrade]; ok {
		r.logger.Info("last upgrade time found", "lastUpgrade", last)

		lastUpgrade, err = time.Parse(time.RFC3339, last)
		if err != nil {
			r.logger.Error("failed to parse LastUpgrade annotation", "err", err)
		}
	}

	delay, err := time.ParseDuration(node.Spec.Upgrade.Delay)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse upgrade delay: %w", err)
	}

	if time.Since(lastUpgrade) < delay {
		return lastUpgrade.Add(delay), nil
	}

	// TODO: Consider a kubernetes drain if we know we are running in a
	// kubernetes cluster.  How do we know?

	if err := r.locker.Lock(ctx, req, labels.LabelUpgradeGroup, node.Spec.Upgrade.Group); err != nil {
		return time.Time{}, fmt.Errorf("failed to acquire upgrade lock: %w", err)
	}

	err = r.system.Package().UpgradeAll(ctx)
	if err != nil {
		return time.Time{}, err
	}

	err = r.system.Node().Upgrade(ctx)
	if err != nil {
		return time.Time{}, err
	}

	r.system.Node().Reboot(ctx)

	return time.Time{}, err
}
