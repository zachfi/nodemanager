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
	"path/filepath"
	"time"

	"github.com/gorhill/cronexpr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/jail"
	"github.com/zachfi/nodemanager/pkg/locker"
)

const jailFinalizer = "freebsd.nodemanager/finalizer"

// Standard condition types for Jail resources.
const (
	condAvailable   = "Available"
	condDegraded    = "Degraded"
	condProgressing = "Progressing"
)

// JailReconciler reconciles Jail objects.
type JailReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	tracer   trace.Tracer
	logger   *slog.Logger
	system   handler.System
	cfg      JailConfig
	hostname string
	locker   locker.Locker

	manager jail.Manager
}

func NewJailReconciler(ctx context.Context, client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg JailConfig, system handler.System, lkr locker.Locker) (*JailReconciler, error) {
	hostname, err := system.Node().Hostname()
	if err != nil {
		return nil, fmt.Errorf("getting local hostname: %w", err)
	}

	manager, err := jail.NewManager(ctx, cfg.JailDataPath, cfg.ZfsDataset, cfg.Mirror, system.Exec())
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
		locker:   lkr,
		manager:  manager,
	}, nil
}

// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails/finalizers,verbs=update
// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jailtemplates,verbs=get;list;watch

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
				jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "delete", "error").Inc()
				_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
					r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "DeleteFailed", err.Error())
				})
				return ctrl.Result{}, err
			}
			jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "delete", "success").Inc()
			controllerutil.RemoveFinalizer(j, jailFinalizer)
			if err := r.Update(ctx, j); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Resolve template defaults if templateRef is set.
	mergedSpec := j.Spec
	var postCreateCmds []freebsdv1.PostCreateCommand
	if j.Spec.TemplateRef != "" {
		tmpl := &freebsdv1.JailTemplate{}
		tmplKey := types.NamespacedName{Name: j.Spec.TemplateRef, Namespace: j.Namespace}
		if err := r.Get(ctx, tmplKey, tmpl); err != nil {
			_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
				r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "TemplateNotFound",
					fmt.Sprintf("JailTemplate %q not found: %v", j.Spec.TemplateRef, err))
			})
			return ctrl.Result{}, err
		}
		mergedSpec = jail.MergeTemplateDefaults(j.Spec, tmpl.Spec)
		postCreateCmds = tmpl.Spec.PostCreate
	}

	// Build a merged jail for provisioning.
	mergedJail := j.DeepCopy()
	mergedJail.Spec = mergedSpec

	if err := r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
		r.setCondition(fresh, condProgressing, metav1.ConditionTrue, "Provisioning", "jail is being provisioned")
	}); err != nil {
		return ctrl.Result{}, err
	}

	provisionStart := time.Now()
	if err := r.manager.EnsureJail(ctx, *mergedJail); err != nil {
		jailProvisionDuration.WithLabelValues(r.hostname, j.Name).Observe(time.Since(provisionStart).Seconds())
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "provision", "error").Inc()
		r.logger.Error("failed to ensure jail", "jail", j.Name, "err", err)
		_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
			r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "EnsureFailed", err.Error())
			r.setCondition(fresh, condProgressing, metav1.ConditionFalse, "EnsureFailed", "provisioning failed")
		})
		return ctrl.Result{}, err
	}
	jailProvisionDuration.WithLabelValues(r.hostname, j.Name).Observe(time.Since(provisionStart).Seconds())
	jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "provision", "success").Inc()

	// Start the jail if it is not already running.
	running, err := r.manager.IsRunning(ctx, j.Name)
	if err != nil {
		r.logger.Error("failed to check jail state", "jail", j.Name, "err", err)
		_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
			r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "StatusCheckFailed", err.Error())
		})
		return ctrl.Result{}, err
	}
	if !running {
		if err := r.manager.StartJail(ctx, j.Name); err != nil {
			jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "start", "error").Inc()
			r.logger.Error("failed to start jail", "jail", j.Name, "err", err)
			_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
				r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "StartFailed", err.Error())
				r.setCondition(fresh, condProgressing, metav1.ConditionFalse, "StartFailed", "jail failed to start")
			})
			return ctrl.Result{}, err
		}
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "start", "success").Inc()
	}

	// Re-query running state after start attempt.
	running, err = r.manager.IsRunning(ctx, j.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	jailRoot := filepath.Join(r.cfg.JailDataPath, jail.JailRootDir, j.Name, "root")

	// Bootstrap pkg(8) if not already present — required for any package
	// operations inside the jail.
	if running {
		if err := r.manager.BootstrapPkg(ctx, j.Name, jailRoot); err != nil {
			r.logger.Error("failed to bootstrap pkg", "jail", j.Name, "err", err)
			_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
				r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "PkgBootstrapFailed", err.Error())
			})
			return ctrl.Result{}, err
		}
	}

	// Run postCreate hooks once after the first successful start.
	if len(postCreateCmds) > 0 && running {
		if j.Status.PostCreateDone == nil {
			for _, cmd := range postCreateCmds {
				r.logger.Info("running postCreate hook", "jail", j.Name, "hook", cmd.Name)
				if err := r.manager.ExecInJail(ctx, j.Name, cmd.Command, cmd.Args...); err != nil {
					jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "postCreate", "error").Inc()
					_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
						r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "PostCreateFailed",
							fmt.Sprintf("postCreate hook %q failed: %v", cmd.Name, err))
					})
					return ctrl.Result{}, err
				}
			}
			jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "postCreate", "success").Inc()

			postCreateNow := metav1.Now()
			if err := r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
				fresh.Status.PostCreateDone = &postCreateNow
			}); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// Populate status.release from the jail root filesystem.
	release := ""
	if rel, err := r.manager.InstalledRelease(jailRoot); err != nil {
		r.logger.Warn("could not read installed release", "jail", j.Name, "err", err)
	} else {
		release = rel
	}

	if err := r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
		r.setCondition(fresh, condProgressing, metav1.ConditionFalse, "Provisioned", "jail provisioned successfully")
		if running {
			r.setCondition(fresh, condAvailable, metav1.ConditionTrue, "Running", "jail is running")
			r.setCondition(fresh, condDegraded, metav1.ConditionFalse, "Running", "")
		} else {
			r.setCondition(fresh, condAvailable, metav1.ConditionFalse, "NotRunning", "jail is not running after start")
			r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "NotRunning", "jail is not running after start attempt")
		}
		if release != "" {
			fresh.Status.Release = release
		}
	}); err != nil {
		return ctrl.Result{}, err
	}

	// Run freebsd-update if a schedule is configured and it is due.
	next, err := r.handleUpdate(ctx, j, jailRoot)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !next.IsZero() {
		return ctrl.Result{RequeueAfter: time.Until(next)}, nil
	}

	return ctrl.Result{}, nil
}

// handleUpdate checks whether a freebsd-update run is due for the jail.
// It mirrors the schedule+delay logic used by ManagedNode.handleUpgrade.
func (r *JailReconciler) handleUpdate(ctx context.Context, j *freebsdv1.Jail, jailRoot string) (time.Time, error) {
	if j.Spec.Update.Schedule == "" || j.Spec.Update.Delay == "" {
		return time.Time{}, nil
	}

	delay, err := time.ParseDuration(j.Spec.Update.Delay)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing jail update delay: %w", err)
	}

	cron, err := cronexpr.Parse(j.Spec.Update.Schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing jail update schedule: %w", err)
	}

	const forgiveness = time.Minute
	next := cron.Next(time.Now().Add(-forgiveness))

	var lockReq types.NamespacedName
	if j.Spec.Update.Group != "" {
		lockReq = types.NamespacedName{Name: j.Spec.Update.Group, Namespace: j.Namespace}

		// If we hold the lock, we've just finished an update — release and requeue.
		if r.locker.Locked(ctx, lockReq) {
			if err := r.locker.Unlock(ctx, lockReq); err != nil {
				r.logger.Warn("failed to release jail update lock", "lease", lockReq, "err", err)
			}
			return next, nil
		}
	}

	// Check last update time from status.
	if j.Status.LastUpdate != nil && time.Since(j.Status.LastUpdate.Time) < delay {
		return next, nil
	}

	// Outside forgiveness window — requeue without running.
	if time.Since(next) < forgiveness {
		// within the window — fall through to run
	} else if time.Until(next) < forgiveness {
		return next, nil
	} else {
		return next, nil
	}

	r.logger.Info("running freebsd-update", "jail", j.Name)

	if j.Spec.Update.Group != "" {
		if err := r.locker.Lock(ctx, lockReq); err != nil {
			return time.Time{}, fmt.Errorf("acquiring jail update lock for %s: %w", j.Name, err)
		}
	}

	if err := r.manager.StopJail(ctx, j.Name); err != nil {
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "stop", "error").Inc()
		return time.Time{}, fmt.Errorf("stopping jail for update: %w", err)
	}
	jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "stop", "success").Inc()

	updateErr := r.manager.UpdateJail(ctx, jailRoot)

	if startErr := r.manager.StartJail(ctx, j.Name); startErr != nil {
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "start", "error").Inc()
		r.logger.Error("failed to restart jail after update", "jail", j.Name, "err", startErr)
	} else {
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "start", "success").Inc()
	}

	if updateErr != nil {
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "update", "error").Inc()
		return time.Time{}, fmt.Errorf("freebsd-update failed for jail %s: %w", j.Name, updateErr)
	}
	jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "update", "success").Inc()

	metaNow := metav1.Now()
	key := types.NamespacedName{Name: j.Name, Namespace: j.Namespace}

	// Record the last-update timestamp on the status subresource.
	if err := r.updateStatusWithRetry(ctx, key, func(fresh *freebsdv1.Jail) {
		fresh.Status.LastUpdate = &metaNow
	}); err != nil {
		return time.Time{}, fmt.Errorf("recording last update status: %w", err)
	}

	return next, nil
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

// updateStatusWithRetry re-fetches the Jail, applies the given mutation, and
// updates the status subresource inside a RetryOnConflict loop so that stale
// resourceVersions do not cause "object has been modified" errors.
func (r *JailReconciler) updateStatusWithRetry(ctx context.Context, key types.NamespacedName, mutate func(*freebsdv1.Jail)) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var fresh freebsdv1.Jail
		if err := r.Get(ctx, key, &fresh); err != nil {
			return err
		}
		mutate(&fresh)
		return r.Status().Update(ctx, &fresh)
	})
}

// jailsReferencingTemplate returns reconcile requests for all Jails in the
// same namespace that reference the changed JailTemplate.
func (r *JailReconciler) jailsReferencingTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	tmpl, ok := obj.(*freebsdv1.JailTemplate)
	if !ok {
		return nil
	}

	var jailList freebsdv1.JailList
	if err := r.List(ctx, &jailList, client.InNamespace(tmpl.Namespace)); err != nil {
		r.logger.Error("failed to list jails for template watch", "err", err)
		return nil
	}

	var requests []reconcile.Request
	for _, j := range jailList.Items {
		if j.Spec.TemplateRef == tmpl.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: j.Name, Namespace: j.Namespace},
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *JailReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.Jail{}).
		Watches(&freebsdv1.JailTemplate{},
			ctrlhandler.EnqueueRequestsFromMapFunc(r.jailsReferencingTemplate)).
		Named("freebsd-jail").
		Complete(r)
}
