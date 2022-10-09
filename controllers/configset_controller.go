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
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/go-logr/logr"
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

	err := nodeLabelMatch(ctx, log, r, r, req, configSet.Labels)
	if err != nil {
		return ctrl.Result{}, nil
	}

	packageHandler, err := common.GetPackageHandler()
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handlePackageSet(log, packageHandler, configSet.Spec.Packages)
	if err != nil {
		return ctrl.Result{}, err
	}

	fileHandler, err := common.GetFileHandler()
	if err != nil {
		return ctrl.Result{}, err
	}

	changedFiles, err := r.handleFileSet(log, fileHandler, configSet.Spec.Files)
	if err != nil {
		return ctrl.Result{}, err
	}

	serviceHandler, err := common.GetServiceHandler()
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleServiceSet(log, serviceHandler, configSet.Spec.Services, changedFiles)
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

func (r *ConfigSetReconciler) handlePackageSet(log logr.Logger, handler common.PackageHandler, packageSet []commonv1.Package) error {

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
			if !currentlyInstalled(packages, pkg.Name) {
				log.Info("installing package", "name", pkg.Name)
				err := handler.Install(pkg.Name)
				if err != nil {
					return err
				}
			}
		case "absent":
			if currentlyInstalled(packages, pkg.Name) {
				err := handler.Remove(pkg.Name)
				log.Info("removing package", "name", pkg.Name)
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

func (r *ConfigSetReconciler) handleServiceSet(log logr.Logger, handler common.ServiceHandler, serviceSet []commonv1.Service, changedFiles []string) error {

	var totalErrs error
	var restartServices []string

	for _, cf := range changedFiles {
		for _, svc := range serviceSet {
			for _, sub := range svc.SusbscribeFiles {
				if sub == cf {
					restartServices = append(restartServices, svc.Name)
				}
			}
		}
	}

	for _, restart := range restartServices {
		err := handler.Restart(restart)
		log.Info("restarting service", "name", restart)
		if err != nil {
			totalErrs = fmt.Errorf("%w: %s", totalErrs, err.Error())
		}
	}

	return totalErrs
}

// handleFileSet
func (r *ConfigSetReconciler) handleFileSet(log logr.Logger, handler common.FileHandler, fileSet []commonv1.File) (changedFiles []string, err error) {
	for _, file := range fileSet {

		var ensure common.FileEnsure
		if file.Ensure == "" {
			ensure = common.File
		} else {
			ensure = common.FileEnsureFromString(file.Ensure)
			if ensure == common.UnhandledFileEnsure {
				return changedFiles, fmt.Errorf("unhandled file ensure %q", file.Ensure)
			}
		}

		switch ensure {
		case common.File:
			if file.Content != "" {
				// Determine the sha of the content
				var b bytes.Buffer
				_, err = b.WriteString(file.Content)
				if err != nil {
					return changedFiles, err
				}

				chsh := sha256.New()
				contentHash := fmt.Sprintf("%x", chsh.Sum(b.Bytes()))

				// Read the sha of the file
				fileBytes, err := os.ReadFile(file.Path)
				if err != nil {
					return changedFiles, err
				}
				fhsh := sha256.New()
				fileHash := fmt.Sprintf("%x", fhsh.Sum(fileBytes))

				// Write only when necessary
				if contentHash != fileHash {
					log.Info(fmt.Sprintf("writing file %q (%x)", file.Path, contentHash))
					err := handler.WriteContentFile(file.Path, []byte(file.Content))
					if err != nil {
						return changedFiles, err
					}
					changedFiles = append(changedFiles, file.Path)
				}
			}

		case common.Directory:
			if _, err := os.Stat(file.Path); os.IsNotExist(err) {
				var fileMode os.FileMode

				if file.Mode == "" {
					fileMode = os.FileMode(0660)
				} else {
					fileMode, err = common.GetFileModeFromString(file.Mode)
					if err != nil {
						return changedFiles, err
					}
				}

				err := os.Mkdir(file.Path, fileMode)
				if err != nil {
					return changedFiles, err
				}
				changedFiles = append(changedFiles, file.Path)
			}

		case common.Symlink:
			target, err := os.Readlink(file.Path)
			if err != nil {
				return changedFiles, err
			}

			if target != file.Target {
				log.Info("symlinking file", "name", file.Path)

				if _, err := os.Lstat(file.Path); err == nil {
					os.Remove(file.Path)
				}

				err := os.Symlink(file.Target, file.Path)
				if err != nil {
					return changedFiles, err
				}
			}
		}
	}

	return
}
