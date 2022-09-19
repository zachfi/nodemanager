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
	"os/exec"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonv1 "znet/nodemanager/api/v1"
	"znet/nodemanager/pkg/common"
)

// ManagedNodeReconciler reconciles a ManagedNode object
type ManagedNodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=common.znet,resources=managednodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.znet,resources=managednodes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.znet,resources=managednodes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ManagedNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

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

	node.Status = nodeStatus(log, req)

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

func nodeStatus(log logr.Logger, req ctrl.Request) commonv1.ManagedNodeStatus {
	var status commonv1.ManagedNodeStatus

	status.OS = common.OsReleaseID()

	releaseCmd := exec.Command("/usr/bin/uname", "-r")
	output, err := releaseCmd.Output()
	if err != nil {
		log.Error(err, "failed to execute uname")
	}
	status.Version = strings.TrimSpace(string(output))

	archCmd := exec.Command("/usr/bin/uname", "-m")
	archOut, err := archCmd.Output()
	if err != nil {
		log.Error(err, "failed to execute uname")
	}
	status.Architecture = strings.TrimSpace(string(archOut))

	return status
}
