/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonv1 "github.com/zachfi/nodemanager/api/v1"
	"github.com/zachfi/nodemanager/pkg/common"
)

// ManagedNodeReconciler reconciles a ManagedNode object
type ManagedNodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Tracer trace.Tracer
}

//+kubebuilder:rbac:groups=common.nodemanager,resources=managednodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.nodemanager,resources=managednodes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.nodemanager,resources=managednodes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ManagedNodeReconciler) Reconcile(rctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error
	log := log.FromContext(rctx)

	attributes := []attribute.KeyValue{
		attribute.String("req", req.String()),
		attribute.String("namespace", req.Namespace),
	}

	ctx, span := r.Tracer.Start(rctx, "Reconcile", trace.WithAttributes(attributes...))

	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	hostname, err := os.Hostname()
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if hostname != req.Name {
		return ctrl.Result{}, nil
	}

	var node commonv1.ManagedNode
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	labels := defaultLabels()

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
		log.Info("updating labels", "labels", nodeLabels)
		if err := r.Update(ctx, &node); err != nil {
			log.Error(err, "unable to update ManagedNode")
			return ctrl.Result{}, err
		}
	}

	node.Status = nodeStatus()
	log.Info("updating node status", "node", hostname, "status", fmt.Sprintf("%+v", node.Status))
	if err := r.Status().Update(ctx, &node); err != nil {
		log.Error(err, "unable to update ManagedNode status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1.ManagedNode{}).
		Complete(r)
}

func nodeStatus() commonv1.ManagedNodeStatus {
	var status commonv1.ManagedNodeStatus
	resolver := &common.UnameInfoResolver{}
	info := resolver.Info()
	status.Release = info.OS.Release
	return status
}

func defaultLabels() map[string]string {
	resolver := &common.UnameInfoResolver{}
	info := resolver.Info()

	return map[string]string{
		"kubernetes.io/os":       info.OS.ID,
		"kubernetes.io/arch":     info.Machine,
		"kubernetes.io/hostname": info.Name,
	}
}
