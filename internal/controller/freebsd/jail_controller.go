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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

	tracer         trace.Tracer
	logger         *slog.Logger
	system         handler.System
	cfg            JailConfig
	hostname       string
	locker         locker.Locker
	controllerName string // defaults to "freebsd-jail"; override in tests to avoid metric name conflicts

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

func (r *JailReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	_ = logf.FromContext(ctx)

	// Open a top-level span for the reconcile.  Sub-spans for each step inside
	// (status updates, EnsureJail, IsRunning, StartJail, BootstrapPkg,
	// postCreate, handleUpdate, requeue path) hang off this one so an operator
	// can see the entire code path for a single reconcile in Tempo.
	ctx, span := r.tracer.Start(ctx, "JailReconciler.Reconcile",
		trace.WithAttributes(
			attribute.String("jail.name", req.Name),
			attribute.String("jail.namespace", req.Namespace),
			attribute.String("host.hostname", r.hostname),
		))
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.SetAttributes(
			attribute.Float64("result.requeue_after_seconds", result.RequeueAfter.Seconds()),
		)
		span.End()
	}()

	// Build a per-reconcile logger that includes the trace and span IDs so
	// log lines can be cross-referenced with the spans in Tempo.
	sc := span.SpanContext()
	logger := r.logger.With(
		"jail", req.Name,
		"trace_id", sc.TraceID().String(),
		"span_id", sc.SpanID().String(),
	)

	j := &freebsdv1.Jail{}
	if err := r.Get(ctx, req.NamespacedName, j); err != nil {
		if client.IgnoreNotFound(err) == nil {
			span.AddEvent("jail not found, ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	span.SetAttributes(
		attribute.String("jail.spec.node_name", j.Spec.NodeName),
		attribute.String("jail.spec.release", j.Spec.Release),
		attribute.String("jail.spec.template_ref", j.Spec.TemplateRef),
		attribute.String("jail.spec.reconcile_period", j.Spec.ReconcilePeriod),
		attribute.Int64("jail.metadata.generation", j.Generation),
		attribute.String("jail.metadata.resource_version", j.ResourceVersion),
	)

	// Only reconcile jails assigned to this host.
	if j.Spec.NodeName != r.hostname {
		span.AddEvent("skipping jail assigned to different node",
			trace.WithAttributes(attribute.String("assigned.node", j.Spec.NodeName)))
		return ctrl.Result{}, nil
	}

	if j.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(j, jailFinalizer) {
			err := r.tracedAddFinalizer(ctx, j)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		span.AddEvent("jail is being deleted")
		return r.handleDeletion(ctx, req, j, logger)
	}

	// Resolve template defaults if templateRef is set.
	mergedSpec := j.Spec
	var postCreateCmds []freebsdv1.PostCreateCommand
	if j.Spec.TemplateRef != "" {
		tmpl, err := r.tracedFetchTemplate(ctx, j)
		if err != nil {
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

	if err := r.tracedSetCondition(ctx, req.NamespacedName, "Progressing=true", func(fresh *freebsdv1.Jail) {
		r.setCondition(fresh, condProgressing, metav1.ConditionTrue, "Provisioning", "jail is being provisioned")
	}); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("provisioning jail with merged spec",
		"interface", mergedJail.Spec.Interface,
		"inets", mergedJail.Spec.Inets,
		"inet6s", mergedJail.Spec.Inet6s)

	provisionStart := time.Now()
	if err := r.manager.EnsureJail(ctx, *mergedJail); err != nil {
		jailProvisionDuration.WithLabelValues(r.hostname, j.Name).Observe(time.Since(provisionStart).Seconds())
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "provision", "error").Inc()
		logger.Error("failed to ensure jail", "err", err)
		_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
			r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "EnsureFailed", err.Error())
			r.setCondition(fresh, condProgressing, metav1.ConditionFalse, "EnsureFailed", "provisioning failed")
		})
		return ctrl.Result{}, err
	}
	jailProvisionDuration.WithLabelValues(r.hostname, j.Name).Observe(time.Since(provisionStart).Seconds())
	jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "provision", "success").Inc()
	span.SetAttributes(attribute.Float64("ensure_jail.duration_seconds", time.Since(provisionStart).Seconds()))

	// Start the jail if it is not already running.
	running, err := r.manager.IsRunning(ctx, j.Name)
	if err != nil {
		logger.Error("failed to check jail state", "err", err)
		_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
			r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "StatusCheckFailed", err.Error())
		})
		return ctrl.Result{}, err
	}
	span.SetAttributes(attribute.Bool("jail.running_before_start", running))

	if !running {
		span.AddEvent("starting jail")
		if err := r.manager.StartJail(ctx, *mergedJail); err != nil {
			jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "start", "error").Inc()
			logger.Error("failed to start jail", "err", err)
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
	span.SetAttributes(attribute.Bool("jail.running_after_start", running))

	jailRoot := filepath.Join(r.cfg.JailDataPath, jail.JailRootDir, j.Name, "root")

	// Bootstrap pkg(8) if not already present — required for any package
	// operations inside the jail.
	if running {
		if err := r.manager.BootstrapPkg(ctx, j.Name, jailRoot); err != nil {
			logger.Error("failed to bootstrap pkg", "err", err)
			_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
				r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "PkgBootstrapFailed", err.Error())
			})
			return ctrl.Result{}, err
		}
	}

	// Run postCreate hooks once after the first successful start.
	if len(postCreateCmds) > 0 && running {
		if j.Status.PostCreateDone == nil {
			if err := r.runPostCreateHooks(ctx, req, j, postCreateCmds, logger); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// Populate status.release from the jail root filesystem.
	release := ""
	if rel, err := r.manager.InstalledRelease(jailRoot); err != nil {
		logger.Warn("could not read installed release", "err", err)
	} else {
		release = rel
	}
	span.SetAttributes(attribute.String("jail.installed_release", release))

	if err := r.tracedSetCondition(ctx, req.NamespacedName, "final", func(fresh *freebsdv1.Jail) {
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

	if j.Spec.ReconcilePeriod != "" {
		period, err := time.ParseDuration(j.Spec.ReconcilePeriod)
		if err != nil {
			r.logger.Warn("invalid reconcilePeriod, skipping periodic requeue", "jail", j.Name, "err", err)
		} else if period > 0 {
			return ctrl.Result{RequeueAfter: period}, nil
		}
	}

	return ctrl.Result{}, nil
}

// tracedAddFinalizer adds the controller's finalizer and pushes the metadata
// update to the API server.  Wrapped in its own span so that an unexpected
// generation bump or webhook denial here is plainly visible in Tempo.
func (r *JailReconciler) tracedAddFinalizer(ctx context.Context, j *freebsdv1.Jail) (err error) {
	ctx, span := r.tracer.Start(ctx, "JailReconciler.AddFinalizer",
		trace.WithAttributes(attribute.String("jail.name", j.Name)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	controllerutil.AddFinalizer(j, jailFinalizer)
	return r.Update(ctx, j)
}

// handleDeletion runs the deletion path: refuse if deletionProtection is set,
// otherwise call DeleteJail and remove the finalizer.
func (r *JailReconciler) handleDeletion(ctx context.Context, req ctrl.Request, j *freebsdv1.Jail, logger *slog.Logger) (result ctrl.Result, retErr error) {
	ctx, span := r.tracer.Start(ctx, "JailReconciler.handleDeletion",
		trace.WithAttributes(
			attribute.String("jail.name", j.Name),
			attribute.Bool("deletion_protection", j.Spec.DeletionProtection),
		))
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	if !controllerutil.ContainsFinalizer(j, jailFinalizer) {
		span.AddEvent("finalizer absent, nothing to do")
		return ctrl.Result{}, nil
	}
	if j.Spec.DeletionProtection {
		logger.Warn("deletion blocked by deletionProtection; set spec.deletionProtection=false to allow")
		span.AddEvent("deletion blocked by deletionProtection")
		_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
			r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "DeletionProtected",
				"deletion blocked: set spec.deletionProtection=false to allow removal")
		})
		return ctrl.Result{}, nil
	}
	if err := r.manager.DeleteJail(ctx, *j); err != nil {
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "delete", "error").Inc()
		_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
			r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "DeleteFailed", err.Error())
		})
		return ctrl.Result{}, err
	}
	jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "delete", "success").Inc()
	span.AddEvent("removing finalizer")
	controllerutil.RemoveFinalizer(j, jailFinalizer)
	if err := r.Update(ctx, j); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// tracedFetchTemplate fetches the JailTemplate referenced by j.Spec.TemplateRef.
func (r *JailReconciler) tracedFetchTemplate(ctx context.Context, j *freebsdv1.Jail) (tmpl *freebsdv1.JailTemplate, err error) {
	ctx, span := r.tracer.Start(ctx, "JailReconciler.FetchTemplate",
		trace.WithAttributes(
			attribute.String("jail.name", j.Name),
			attribute.String("template.ref", j.Spec.TemplateRef),
		))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	tmpl = &freebsdv1.JailTemplate{}
	tmplKey := types.NamespacedName{Name: j.Spec.TemplateRef, Namespace: j.Namespace}
	if err = r.Get(ctx, tmplKey, tmpl); err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.Int64("template.generation", tmpl.Generation))
	return tmpl, nil
}

// tracedSetCondition wraps updateStatusWithRetry in a span so each status
// write is visible in the trace.  The label argument is only attached to the
// span (not stored in the object) and is meant to identify the call-site.
func (r *JailReconciler) tracedSetCondition(ctx context.Context, key types.NamespacedName, label string, mutate func(*freebsdv1.Jail)) (err error) {
	ctx, span := r.tracer.Start(ctx, "JailReconciler.UpdateStatus",
		trace.WithAttributes(
			attribute.String("jail.name", key.Name),
			attribute.String("status.label", label),
		))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	return r.updateStatusWithRetry(ctx, key, mutate)
}

// runPostCreateHooks runs each postCreate hook sequentially inside the jail
// and records the completion time on status.PostCreateDone.
func (r *JailReconciler) runPostCreateHooks(ctx context.Context, req ctrl.Request, j *freebsdv1.Jail, hooks []freebsdv1.PostCreateCommand, logger *slog.Logger) (err error) {
	ctx, span := r.tracer.Start(ctx, "JailReconciler.runPostCreateHooks",
		trace.WithAttributes(
			attribute.String("jail.name", j.Name),
			attribute.Int("hooks", len(hooks)),
		))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	for _, cmd := range hooks {
		logger.Info("running postCreate hook", "hook", cmd.Name)
		span.AddEvent("postCreate hook",
			trace.WithAttributes(
				attribute.String("hook.name", cmd.Name),
				attribute.String("hook.command", cmd.Command),
			))
		if err = r.manager.ExecInJail(ctx, j.Name, cmd.Command, cmd.Args...); err != nil {
			jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "postCreate", "error").Inc()
			_ = r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
				r.setCondition(fresh, condDegraded, metav1.ConditionTrue, "PostCreateFailed",
					fmt.Sprintf("postCreate hook %q failed: %v", cmd.Name, err))
			})
			return err
		}
	}
	jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "postCreate", "success").Inc()

	postCreateNow := metav1.Now()
	return r.updateStatusWithRetry(ctx, req.NamespacedName, func(fresh *freebsdv1.Jail) {
		fresh.Status.PostCreateDone = &postCreateNow
	})
}

// handleUpdate checks whether a freebsd-update run is due for the jail.
// This always runs on the HOST — it stops the jail, applies patch-level OS
// updates to the jail's root filesystem via freebsd-update(8), then restarts
// the jail.  A nodemanager instance running INSIDE a jail should not set
// spec.update.schedule; only the host nodemanager (spec.nodeName matching the
// physical host) should manage jail OS updates.
// It mirrors the schedule+delay logic used by ManagedNode.handleUpgrade.
func (r *JailReconciler) handleUpdate(ctx context.Context, j *freebsdv1.Jail, jailRoot string) (next time.Time, retErr error) {
	if j.Spec.Update.Schedule == "" || j.Spec.Update.Delay == "" {
		return time.Time{}, nil
	}

	ctx, span := r.tracer.Start(ctx, "JailReconciler.handleUpdate",
		trace.WithAttributes(
			attribute.String("jail.name", j.Name),
			attribute.String("update.schedule", j.Spec.Update.Schedule),
			attribute.String("update.delay", j.Spec.Update.Delay),
			attribute.String("update.group", j.Spec.Update.Group),
		))
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		if !next.IsZero() {
			span.SetAttributes(attribute.String("next_scheduled", next.Format(time.RFC3339)))
		}
		span.End()
	}()

	delay, err := time.ParseDuration(j.Spec.Update.Delay)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing jail update delay: %w", err)
	}

	schedExpr, err := cronexpr.Parse(j.Spec.Update.Schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing jail update schedule: %w", err)
	}

	const forgiveness = time.Minute
	next = schedExpr.Next(time.Now().Add(-forgiveness))

	var lockReq types.NamespacedName
	if j.Spec.Update.Group != "" {
		lockReq = types.NamespacedName{Name: j.Spec.Update.Group, Namespace: j.Namespace}

		// If we hold the lock from a previous update, release it so the next
		// group member can take its turn.
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

	// Only run when within the forgiveness window past the scheduled time.
	// time.Since(next) is positive when next is in the past.
	sinceNext := time.Since(next)
	if sinceNext < 0 || sinceNext >= forgiveness {
		return next, nil
	}

	r.logger.Info("running freebsd-update", "jail", j.Name)

	if j.Spec.Update.Group != "" {
		lockTTL := time.Until(schedExpr.Next(time.Now()))
		if err := r.locker.LockFor(ctx, lockReq, lockTTL); err != nil {
			if apierrors.IsConflict(err) {
				r.logger.Info("jail update group lock held by another member, skipping this slot",
					"group", j.Spec.Update.Group, "jail", j.Name)
				return next, nil
			}
			return time.Time{}, fmt.Errorf("acquiring jail update lock for %s: %w", j.Name, err)
		}
	}

	if err := r.manager.StopJail(ctx, j.Name); err != nil {
		jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "stop", "error").Inc()
		if j.Spec.Update.Group != "" {
			if unlockErr := r.locker.Unlock(ctx, lockReq); unlockErr != nil {
				r.logger.Warn("failed to release jail update lock after stop failure", "lease", lockReq, "err", unlockErr)
			}
		}
		return time.Time{}, fmt.Errorf("stopping jail for update: %w", err)
	}
	jailOperationsTotal.WithLabelValues(r.hostname, j.Name, "stop", "success").Inc()

	updateErr := r.manager.UpdateJail(ctx, jailRoot)

	startErr := r.manager.StartJail(ctx, *j)
	if startErr != nil {
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

	if startErr != nil {
		return time.Time{}, fmt.Errorf("restarting jail after update: %w", startErr)
	}

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
	// Only reconcile on generation changes (spec edits), not on status-only
	// updates. All four functions must be set explicitly: leaving GenericFunc
	// nil defaults to true, letting generic events bypass the filter and
	// recreating the tight loop.
	genChanged := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration()
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	// For JailTemplate: only propagate changes when the template spec actually
	// changes (generation bump).  This filters informer resync events where old
	// and new objects are identical, preventing a tight reconcile loop when many
	// jails reference the same template.
	tmplChanged := predicate.GenerationChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.Jail{}, ctrlbuilder.WithPredicates(genChanged)).
		Watches(&freebsdv1.JailTemplate{},
			ctrlhandler.EnqueueRequestsFromMapFunc(r.jailsReferencingTemplate),
			ctrlbuilder.WithPredicates(tmplChanged)).
		Named(func() string {
			if r.controllerName != "" {
				return r.controllerName
			}
			return "freebsd-jail"
		}()).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
			RateLimiter: workqueue.NewTypedMaxOfRateLimiter(
				workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](30*time.Second, 5*time.Minute),
				&workqueue.TypedBucketRateLimiter[reconcile.Request]{
					Limiter: rate.NewLimiter(rate.Every(30*time.Second), 1),
				},
			),
		}).
		Complete(r)
}
