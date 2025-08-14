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
	"log/slog"

	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/zachfi/nodemanager/pkg/common/labels"
	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/poudriere"
	"github.com/zachfi/nodemanager/pkg/util"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// PoudriereReconciler reconciles a Poudriere object
type PoudriereReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	tracer trace.Tracer
	logger *slog.Logger
	system handler.System
}

//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PoudriereReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	node, err := util.GetNode(ctx, r, req, r.system.Node())
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Bail if this node is disabled
	gate := labels.LabelGate(labels.NoneMatch, node.Labels, map[string]string{labels.PoudriereBuild: "disabled"})
	if gate {
		return ctrl.Result{}, err
	}

	// Match only the key
	gate = labels.LabelGate(labels.AnyKey, node.Labels, map[string]string{labels.PoudriereBuild: ""})
	if !gate {
		return ctrl.Result{}, err
	}

	exec := &execs.ExecHandlerCommon{}

	p, err := poudriere.NewPorts(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.ensureAllTrees(ctx, p)
	if err != nil {
		return ctrl.Result{}, err
	}

	j, err := poudriere.NewJail(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.ensureAllJails(ctx, j)
	if err != nil {
		return ctrl.Result{}, err
	}

	b, err := poudriere.NewBulk(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.buildAll(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PoudriereReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		// For().
		Complete(r)
}

func (r *PoudriereReconciler) WithTracer(tracer trace.Tracer) {
	r.tracer = tracer
}

func (r *PoudriereReconciler) WithLogger(logger *slog.Logger) {
	r.logger = logger
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

func (r *PoudriereReconciler) buildAll(ctx context.Context, p *poudriere.PoudriereBulk) error {
	bulks := &freebsdv1.PoudriereBulkList{}
	err := r.List(ctx, bulks)
	if err != nil {
		return err
	}

	err = p.Sync(ctx)
	if err != nil {
		return err
	}

	for _, b := range bulks.Items {
		p.Build(ctx, b.Spec.Jail, b.Spec.Tree, b.Spec.Ports)
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
