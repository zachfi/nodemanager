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

package freebsd

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/jail"
)

const jailFinalizer = "freebsd.nodemanager/finalizer"

// JailReconciler reconciles Jail objects.
type JailReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	tracer   trace.Tracer
	logger   *slog.Logger
	system   handler.System
	cfg      JailConfig
	hostname string

	manager jail.Manager
}

func NewJailReconciler(ctx context.Context, client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg JailConfig, system handler.System) (*JailReconciler, error) {
	hostname, err := system.Node().Hostname()
	if err != nil {
		return nil, fmt.Errorf("getting local hostname: %w", err)
	}

	manager, err := jail.NewManager(ctx, cfg.JailDataPath, cfg.ZfsDataset, system.Exec())
	if err != nil {
		return nil, fmt.Errorf("creating jail manager: %w", err)
	}

	return &JailReconciler{
		Client:   client,
		Scheme:   scheme,
		tracer:   otel.Tracer("controller.freebsd.jail"),
		logger:   logger.With("controller", "jail"),
		system:   system,
		cfg:      cfg,
		hostname: hostname,
		manager:  manager,
	}, nil
}

// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails/finalizers,verbs=update

func (r *JailReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	j := &freebsdv1.Jail{}
	if err := r.Get(ctx, req.NamespacedName, j); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only reconcile jails assigned to this host.
	if j.Spec.NodeName != r.hostname {
		return ctrl.Result{}, nil
	}

	if j.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(j, jailFinalizer) {
			controllerutil.AddFinalizer(j, jailFinalizer)
			if err := r.Update(ctx, j); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(j, jailFinalizer) {
			if err := r.manager.DeleteJail(ctx, *j); err != nil {
				r.setCondition(j, "Degraded", metav1.ConditionTrue, "DeleteFailed", err.Error())
				_ = r.Status().Update(ctx, j)
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(j, jailFinalizer)
			if err := r.Update(ctx, j); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	r.setCondition(j, "Progressing", metav1.ConditionTrue, "Provisioning", "jail is being provisioned")
	if err := r.Status().Update(ctx, j); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.manager.EnsureJail(ctx, *j); err != nil {
		r.logger.Error("failed to ensure jail", "jail", j.Name, "err", err)
		r.setCondition(j, "Degraded", metav1.ConditionTrue, "EnsureFailed", err.Error())
		r.setCondition(j, "Progressing", metav1.ConditionFalse, "EnsureFailed", "provisioning failed")
		_ = r.Status().Update(ctx, j)
		return ctrl.Result{}, err
	}

	// Start the jail if it is not already running.
	running, err := r.manager.IsRunning(ctx, j.Name)
	if err != nil {
		r.logger.Error("failed to check jail state", "jail", j.Name, "err", err)
		r.setCondition(j, "Degraded", metav1.ConditionTrue, "StatusCheckFailed", err.Error())
		_ = r.Status().Update(ctx, j)
		return ctrl.Result{}, err
	}
	if !running {
		if err := r.manager.StartJail(ctx, j.Name); err != nil {
			r.logger.Error("failed to start jail", "jail", j.Name, "err", err)
			r.setCondition(j, "Degraded", metav1.ConditionTrue, "StartFailed", err.Error())
			r.setCondition(j, "Progressing", metav1.ConditionFalse, "StartFailed", "jail failed to start")
			_ = r.Status().Update(ctx, j)
			return ctrl.Result{}, err
		}
	}

	// Re-query running state after start attempt.
	running, err = r.manager.IsRunning(ctx, j.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	r.setCondition(j, "Progressing", metav1.ConditionFalse, "Provisioned", "jail provisioned successfully")
	if running {
		r.setCondition(j, "Available", metav1.ConditionTrue, "Running", "jail is running")
		r.setCondition(j, "Degraded", metav1.ConditionFalse, "Running", "")
	} else {
		r.setCondition(j, "Available", metav1.ConditionFalse, "NotRunning", "jail is not running after start")
		r.setCondition(j, "Degraded", metav1.ConditionTrue, "NotRunning", "jail is not running after start attempt")
	}
	if err := r.Status().Update(ctx, j); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// setCondition upserts a named condition on the jail's status.
func (r *JailReconciler) setCondition(j *freebsdv1.Jail, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&j.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: j.Generation,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *JailReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.Jail{}).
		Named("freebsd-jail").
		Complete(r)
}
