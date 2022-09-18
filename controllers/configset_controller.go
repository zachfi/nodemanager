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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonv1 "znet/nodemanager/api/v1"

	"znet/nodemanager/pkg/common"
)

// ConfigSetReconciler reconciles a ConfigSet object
type ConfigSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=common.znet,resources=configsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.znet,resources=configsets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.znet,resources=configsets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ConfigSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var configSet commonv1.ConfigSet
	if err := r.Get(ctx, req.NamespacedName, &configSet); err != nil {
		log.Error(err, "failed to get resource")

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := nodeLabelMatch(ctx, r, req, configSet.ObjectMeta.Labels)
	if err != nil {
		log.Error(err, "node labels did not match")
		return ctrl.Result{}, nil
	}

	packageHandler, err := common.GetPackageHandler()
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handlePackageSet(packageHandler, configSet.Spec.Packages)
	if err != nil {
		return ctrl.Result{}, err
	}

	serviceHandler, err := common.GetServiceHandler()
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleServiceSet(serviceHandler, configSet.Spec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleFileSet(fileHandler, configSet.Spec.Files)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1.ConfigSet{}).
		Complete(r)
}

func (r *ConfigSetReconciler) handlePackageSet(handler common.PackageHandler, packageSet []commonv1.Package) error {

	currentlyInstalled := func(packages []string, pkg string) bool {
		for _, p := range packages {
			if p == pkg {
				return true
			}
		}

		return false
	}

	packages, err := handler.List()
	if err != nil {
		return err
	}

	for _, pkg := range packageSet {

		switch pkg.Ensure {
		case "installed":
			if currentlyInstalled(packages, pkg.Name) {
				err := handler.Install(pkg.Name)
				if err != nil {
					return err
				}
			}
		case "absent":
			if currentlyInstalled(packages, pkg.Name) {
				err := handler.Remove(pkg.Name)
				if err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unhandled Ensure value %q for package %q", pkg.Ensure, pkg.Name)
		}
	}

	return nil
}

func (r *ConfigSetReconciler) handleServiceSet(handler common.ServiceHandler, serviceSet []commonv1.Service) error {

	return nil
}

func (r *ConfigSetReconciler) handleFileSet(handler common.FileHandler, serviceSet []commonv1.File) error {

	return nil
}
