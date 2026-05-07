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
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/internal/controller/limits"
	"github.com/zachfi/nodemanager/pkg/common/labels"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/poudriere"
)

// PoudriereReconciler reconciles a Poudriere object
type PoudriereReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	tracer trace.Tracer
	logger *slog.Logger
	system handler.System
	cfg    PoudriereConfig
}

func NewPoudriereReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg PoudriereConfig, system handler.System) *PoudriereReconciler {
	return &PoudriereReconciler{
		Client: client,
		Scheme: scheme,
		tracer: otel.Tracer("controller.freebsd.poudriere"),
		logger: logger.With("controller", "poudriere"),
		system: system,
		cfg:    cfg,
	}
}

//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres/finalizers,verbs=update

// Reconcile drives a single PoudriereBulk through the build pipeline:
//
//  1. label-gate the host on freebsd.nodemanager/poudriere
//  2. ensure all PoudrierePorts trees exist (poudriere ports -c)
//  3. ensure all PoudriereJail build-jails exist (poudriere jail -c)
//  4. portshaker -v to pull port-tree changes
//  5. poudriere bulk for the specific Bulk in req
//  6. write status (LastBuildTime / LastBuildResult / Conditions) and
//     record Prometheus metrics
//  7. requeue after Spec.ReconcilePeriod for periodic rebuilds
//
// Steps 2 and 3 act on the full namespace because the per-bulk reconcile
// path needs the full setup to be in place; they are idempotent.
func (r *PoudriereReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	hostname, err := r.system.Node().Hostname()
	if err != nil {
		return ctrl.Result{}, err
	}

	var node commonv1.ManagedNode
	if err := r.Get(ctx, types.NamespacedName{Name: hostname, Namespace: req.Namespace}, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Bail if this node has the poudriere label explicitly set to "disabled".
	// labels.Or returns true on the first matching key+value, which is exactly
	// "this label is set to disabled."  The previous use of labels.NoneMatch
	// was inverted: NoneMatch returns true only when the value does NOT match,
	// so the reconciler bailed on every node whose poudriere label was anything
	// other than "disabled" — meaning the reconciler effectively never ran on
	// a correctly-configured build host.
	if labels.LabelGate(labels.Or, node.Labels, map[string]string{labels.PoudriereBuild: "disabled"}) {
		return ctrl.Result{}, nil
	}

	// Require the poudriere label to be present at all (any non-"disabled"
	// value opts the node in).
	if !labels.LabelGate(labels.AnyKey, node.Labels, map[string]string{labels.PoudriereBuild: ""}) {
		return ctrl.Result{}, nil
	}

	// Fetch the specific Bulk being reconciled.  If it's gone (deletion),
	// return without touching trees/jails — there's nothing to record
	// status against.
	var bulk freebsdv1.PoudriereBulk
	if err := r.Get(ctx, req.NamespacedName, &bulk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	exec := r.system.Exec()

	p, err := poudriere.NewPorts(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureAllTrees(ctx, p); err != nil {
		r.recordBuildFailure(ctx, req.NamespacedName, hostname, "TreesEnsureFailed", err)
		return ctrl.Result{}, err
	}

	j, err := poudriere.NewJail(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureAllJails(ctx, j); err != nil {
		r.recordBuildFailure(ctx, req.NamespacedName, hostname, "JailsEnsureFailed", err)
		return ctrl.Result{}, err
	}

	b, err := poudriere.NewBulk(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Mark the bulk as Progressing before kicking off the build.
	_ = r.updateBulkStatus(ctx, req.NamespacedName, func(fresh *freebsdv1.PoudriereBulk) {
		r.setBulkCondition(fresh, condProgressing, metav1.ConditionTrue, "Building",
			"syncing port tree and running poudriere bulk")
	})

	start := time.Now()
	buildErr := r.runBuild(ctx, b, &bulk)
	duration := time.Since(start)

	// Always record the run's outcome — success or failure — so operators
	// see a fresh LastBuildTime even on failed runs.
	resultLabel := "success"
	if buildErr != nil {
		resultLabel = "error"
	}
	poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, resultLabel).Inc()
	poudriereBulkDuration.WithLabelValues(hostname, bulk.Name).Observe(duration.Seconds())
	poudriereLastBulkTimestamp.WithLabelValues(hostname, bulk.Name).Set(float64(time.Now().Unix()))

	_ = r.updateBulkStatus(ctx, req.NamespacedName, func(fresh *freebsdv1.PoudriereBulk) {
		now := metav1.Now()
		fresh.Status.LastBuildTime = &now
		if buildErr != nil {
			fresh.Status.LastBuildResult = "Failed"
			fresh.Status.LastError = buildErr.Error()
			r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, "BuildFailed", "build failed")
			r.setBulkCondition(fresh, condDegraded, metav1.ConditionTrue, "BuildFailed", buildErr.Error())
			r.setBulkCondition(fresh, condAvailable, metav1.ConditionFalse, "BuildFailed",
				"no usable build available")
		} else {
			fresh.Status.LastBuildResult = "Succeeded"
			fresh.Status.LastError = ""
			r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, "Built", "build completed")
			r.setBulkCondition(fresh, condDegraded, metav1.ConditionFalse, "Built", "")
			r.setBulkCondition(fresh, condAvailable, metav1.ConditionTrue, "Built",
				"build completed successfully")
		}
	})

	if buildErr != nil {
		// Returning the error lets controller-runtime apply its rate
		// limiter and retry; the next reconcile will write a fresh
		// status when the build either succeeds or fails again.
		return ctrl.Result{}, buildErr
	}

	// Periodic rebuild: requeue after Spec.ReconcilePeriod when set.
	if bulk.Spec.ReconcilePeriod != "" {
		period, err := time.ParseDuration(bulk.Spec.ReconcilePeriod)
		if err != nil {
			r.logger.Warn("invalid reconcilePeriod, ignoring",
				"bulk", bulk.Name, "value", bulk.Spec.ReconcilePeriod, "err", err)
		} else if period > 0 {
			return ctrl.Result{RequeueAfter: period}, nil
		}
	}

	return ctrl.Result{}, nil
}

// runBuild executes portshaker sync followed by `poudriere bulk` for the
// single PoudriereBulk being reconciled.  Errors are wrapped with the failing
// stage so status messages identify which step broke.
func (r *PoudriereReconciler) runBuild(ctx context.Context, b *poudriere.PoudriereBulk, bulk *freebsdv1.PoudriereBulk) error {
	if err := b.Sync(ctx); err != nil {
		return fmt.Errorf("portshaker sync failed: %w", err)
	}
	if err := b.Build(ctx, bulk.Spec.Jail, bulk.Spec.Tree, bulk.Spec.Ports); err != nil {
		return fmt.Errorf("poudriere bulk failed: %w", err)
	}
	return nil
}

// setBulkCondition upserts a named condition on the bulk's status.
func (r *PoudriereReconciler) setBulkCondition(b *freebsdv1.PoudriereBulk, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&b.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: b.Generation,
	})
}

// updateBulkStatus re-fetches the bulk and applies the mutation under
// RetryOnConflict so concurrent status writes from rapid reconciles don't
// produce "object has been modified" errors.
func (r *PoudriereReconciler) updateBulkStatus(ctx context.Context, key types.NamespacedName, mutate func(*freebsdv1.PoudriereBulk)) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var fresh freebsdv1.PoudriereBulk
		if err := r.Get(ctx, key, &fresh); err != nil {
			return err
		}
		mutate(&fresh)
		return r.Status().Update(ctx, &fresh)
	})
}

// recordBuildFailure writes a Degraded condition + LastError when an
// ensure-step fails before the bulk run can start.  It is best-effort: we
// already have a real error from the failing step, so a second error from
// the status write is logged and dropped.
func (r *PoudriereReconciler) recordBuildFailure(ctx context.Context, key types.NamespacedName, hostname, reason string, err error) {
	poudriereBulkRunsTotal.WithLabelValues(hostname, key.Name, "error").Inc()
	if uerr := r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		now := metav1.Now()
		fresh.Status.LastBuildTime = &now
		fresh.Status.LastBuildResult = "Failed"
		fresh.Status.LastError = err.Error()
		r.setBulkCondition(fresh, condDegraded, metav1.ConditionTrue, reason, err.Error())
		r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, reason, "build prerequisites failed")
	}); uerr != nil {
		r.logger.Error("failed to record build failure status", "bulk", key.Name, "err", uerr)
	}
}

// SetupWithManager sets up the controller with the Manager.  Uses the
// LongRunning profile because a successful reconcile can run portshaker
// plus `poudriere bulk`, which routinely takes minutes to hours; the
// 30-second token bucket prevents a status-update loop or a flapping
// resource from kicking off heavy builds back-to-back.
func (r *PoudriereReconciler) SetupWithManager(mgr ctrl.Manager) error {
	opts := controller.Options{}
	limits.LongRunning.ApplyTo(&opts)
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.PoudriereBulk{}).
		Named("Poudriere").
		Watches(&freebsdv1.PoudriereJail{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.bulksOnChange)).
		Watches(&freebsdv1.PoudrierePorts{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.bulksOnChange)).
		WithOptions(opts).
		Complete(r)
}

// bulksOnChange enqueues all PoudriereBulk objects in the same namespace as
// the changed object. This ensures that creating or updating a PoudriereJail
// or PoudrierePorts triggers the reconciler, which processes all of them.
func (r *PoudriereReconciler) bulksOnChange(ctx context.Context, obj client.Object) []reconcile.Request {
	var list freebsdv1.PoudriereBulkList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, len(list.Items))
	for i, b := range list.Items {
		reqs[i] = reconcile.Request{NamespacedName: types.NamespacedName{Name: b.Name, Namespace: b.Namespace}}
	}
	return reqs
}

func (r *PoudriereReconciler) ensureAllTrees(ctx context.Context, p *poudriere.PoudrierePorts) error {
	trees := &freebsdv1.PoudrierePortsList{}
	err := r.List(ctx, trees)
	if err != nil {
		return err
	}

	existing, err := p.List(ctx)
	if err != nil {
		return err
	}

	for _, t := range trees.Items {
		if v, ok := exists(t.Name, existing); ok {
			err = p.Update(ctx, *v)
			if err != nil {
				return err
			}
		} else {
			err = p.Create(ctx, poudriere.PortsTree{
				Name:        t.Name,
				FetchMethod: t.Spec.FetchMethod,
				Branch:      t.Spec.Branch,
			})
			if err != nil {
				return err
			}
		}

		for _, e := range existing {
			if e.Name == t.Name {
				err = p.Update(ctx, *e)
				if err != nil {
					return err
				}
				continue
			}
		}
	}

	return nil
}

func (r *PoudriereReconciler) ensureAllJails(ctx context.Context, p *poudriere.PoudriereJail) error {
	jails := &freebsdv1.PoudriereJailList{}
	err := r.List(ctx, jails)
	if err != nil {
		return err
	}

	existing, err := p.List(ctx)
	if err != nil {
		return err
	}

	for _, t := range jails.Items {
		if v, ok := exists(t.Name, existing); ok {
			err = p.Update(ctx, *v)
			if err != nil {
				return err
			}
		} else {
			err = p.Create(ctx, poudriere.BuildJail{
				Name:        t.Name,
				FetchMethod: "http",
				Version:     t.Spec.Version,
			})
			if err != nil {
				return err
			}
		}

		for _, e := range existing {
			if e.Name == t.Name {
				err = p.Update(ctx, *e)
				if err != nil {
					return err
				}
				continue
			}
		}
	}

	return nil
}

type nameable interface {
	GetName() string
}

func exists[N nameable](s string, l []N) (N, bool) {
	for _, p := range l {
		if p.GetName() == s {
			return p, true
		}
	}

	x := new(N)
	return *x, false
}
