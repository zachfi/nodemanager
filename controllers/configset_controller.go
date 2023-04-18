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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonv1 "github.com/zachfi/nodemanager/api/v1"

	"github.com/zachfi/nodemanager/pkg/common"
)

// ConfigSetReconciler reconciles a ConfigSet object
type ConfigSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Tracer trace.Tracer
}

//+kubebuilder:rbac:groups=common.nodemanager,resources=configsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.nodemanager,resources=configsets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.nodemanager,resources=configsets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ConfigSetReconciler) Reconcile(rctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	var configSet commonv1.ConfigSet
	if err = r.Get(ctx, req.NamespacedName, &configSet); err != nil {
		log.Error(err, "failed to get resource")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	node, err := createOrGetNode(ctx, log, r, r, req)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = nodeLabelMatch(node, configSet.Labels)
	if err != nil {
		err = nil                 // for the span defer
		return ctrl.Result{}, nil // Don't error if the configset doesn't match our label set
	}

	packageHandler, err := common.GetPackageHandler(ctx, r.Tracer, log)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handlePackageSet(ctx, log, packageHandler, configSet.Spec.Packages)
	if err != nil {
		return ctrl.Result{}, err
	}

	fileHandler, err := common.GetFileHandler(ctx, r.Tracer, log)
	if err != nil {
		return ctrl.Result{}, err
	}

	changedFiles, err := r.handleFileSet(ctx, log, req.Namespace, fileHandler, configSet.Spec.Files, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	serviceHandler, err := common.GetServiceHandler(ctx, r.Tracer, log)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleServiceSet(ctx, log, serviceHandler, configSet.Spec.Services, changedFiles)
	if err != nil {
		return ctrl.Result{}, err
	}

	execHandler, err := common.GetExecHandler(ctx, r.Tracer)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleExecutions(ctx, log, execHandler, configSet.Spec.Executions, changedFiles)
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

func (r *ConfigSetReconciler) handlePackageSet(ctx context.Context, log logr.Logger, handler common.PackageHandler, packageSet []commonv1.Package) error {
	ctx, span := r.Tracer.Start(ctx, "handlePackageSet")
	defer span.End()

	currentlyInstalled := func(packages []string, pkg string) bool {
		for _, p := range packages {
			if p == pkg {
				return true
			}
		}

		return false
	}

	packages, err := handler.List(ctx)
	if err != nil {
		return err
	}

	for _, pkg := range packageSet {

		switch pkg.Ensure {
		case "installed":
			if !currentlyInstalled(packages, pkg.Name) {
				err := handler.Install(ctx, pkg.Name)
				if err != nil {
					return err
				}
			}
		case "absent":
			if currentlyInstalled(packages, pkg.Name) {
				err := handler.Remove(ctx, pkg.Name)
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

func (r *ConfigSetReconciler) handleServiceSet(ctx context.Context, log logr.Logger, handler common.ServiceHandler, serviceSet []commonv1.Service, changedFiles []string) error {
	ctx, span := r.Tracer.Start(ctx, "handleServiceSet")
	defer span.End()

	var totalErrs error
	var restartServices []string

	for _, cf := range changedFiles {
		for _, svc := range serviceSet {
			// Only record services for restart that are supposed to be running
			if svc.Ensure == common.Running.String() {
				for _, sub := range svc.SusbscribeFiles {
					if sub == cf {
						restartServices = append(restartServices, svc.Name)
					}
				}
			}
		}
	}

	for _, svc := range serviceSet {
		if svc.Enable {
			err := handler.Enable(ctx, svc.Name)
			if err != nil {
				return errors.Wrap(err, "failed to enable service")
			}
		} else {
			err := handler.Disable(ctx, svc.Name)
			if err != nil {
				return errors.Wrap(err, "failed to disable service")
			}
		}

		if svc.Arguments != "" {
			err := handler.SetArguments(ctx, svc.Name, svc.Arguments)
			if err != nil {
				return errors.Wrap(err, "failed to set service arguments")
			}
		}

		status, _ := handler.Status(ctx, svc.Name)
		switch svc.Ensure {
		case common.Running.String():
			if status != common.Running.String() {
				err := handler.Start(ctx, svc.Name)
				if err != nil {
					return errors.Wrap(err, "failed to start service")
				}
			}
		case common.Stopped.String():
			if status != common.Stopped.String() {
				err := handler.Stop(ctx, svc.Name)
				if err != nil {
					return errors.Wrap(err, "failed to stop service")
				}
			}
		}
	}

	for _, restart := range restartServices {
		err := handler.Restart(ctx, restart)
		log.Info("restarting service", "name", restart)
		if err != nil {
			totalErrs = fmt.Errorf("%w: %s", totalErrs, err.Error())
		}
	}

	return totalErrs
}

// handleFileSet
func (r *ConfigSetReconciler) handleFileSet(ctx context.Context, log logr.Logger, namespace string, handler common.FileHandler, fileSet []commonv1.File, node commonv1.ManagedNode) (changedFiles []string, err error) {
	ctx, span := r.Tracer.Start(ctx, "handleFileSet")
	defer span.End()

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
			// If we have a template, let's set the content based on the rendered template.
			if file.Template != "" {
				data, err := r.collectData(ctx, log, namespace, file, node)
				if err != nil {
					return changedFiles, err
				}

				content, err := r.buildTemplate(ctx, log, file.Template, data)
				if err != nil {
					return changedFiles, err
				}

				if len(content) > 0 {
					file.Content = string(content)
				}
			}

			if file.Content != "" {
				err, changed := r.writeFileContent(ctx, file, log, handler)
				if err != nil {
					return changedFiles, err
				}

				if changed {
					changedFiles = append(changedFiles, file.Path)
				}
			}

		case common.Directory:
			if _, err := os.Stat(file.Path); os.IsNotExist(err) {
				var fileMode os.FileMode

				if file.Mode == "" {
					fileMode = os.FileMode(0660)
				} else {
					fileMode, err = common.GetFileModeFromString(ctx, file.Mode)
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

func (r *ConfigSetReconciler) handleExecutions(ctx context.Context, log logr.Logger, handler common.ExecHandler, serviceSet []commonv1.Exec, changedFiles []string) error {
	ctx, span := r.Tracer.Start(ctx, "handleExecutions")
	defer span.End()

	var totalErrs error
	var runExec []commonv1.Exec

	for _, cf := range changedFiles {
		for _, exe := range serviceSet {
			for _, sub := range exe.SusbscribeFiles {
				if sub == cf {
					runExec = append(runExec, exe)
				}
			}
		}
	}

	for _, exe := range runExec {
		_, _, err := handler.RunCommand(ctx, exe.Command, exe.Args...)
		log.Info("running exec", "command", exe.Command)
		if err != nil {
			totalErrs = fmt.Errorf("%w: %s", totalErrs, err.Error())
		}
	}

	return totalErrs
}

func (r *ConfigSetReconciler) collectData(ctx context.Context, log logr.Logger, namespace string, file commonv1.File, node commonv1.ManagedNode) (data Data, err error) {
	var nodeData NodeData
	nodeData.Labels = node.Labels

	secrets := map[string][]byte{}
	for _, s := range file.SecretRefs {

		// Render the secretRef in case it is a template string
		st, err := r.buildTemplate(ctx, log, s, Data{Node: nodeData})
		if err != nil {
			return Data{}, errors.Wrap(err, "failed to build template string rendering secretRef")
		}

		var secret corev1.Secret
		nsn := types.NamespacedName{
			Name:      string(st),
			Namespace: namespace,
		}
		if err := r.Get(ctx, nsn, &secret); err != nil {
			return Data{}, err
		}

		for k, v := range secret.Data {
			secrets[k] = v
		}
	}
	nodeData.Secrets = secrets

	configMaps := map[string]string{}
	for _, c := range file.ConfigMapRefs {
		var configMap corev1.ConfigMap
		nsn := types.NamespacedName{
			Name:      c,
			Namespace: namespace,
		}
		if err := r.Get(ctx, nsn, &configMap); err != nil {
			return Data{}, err
		}

		for k, v := range configMap.Data {
			configMaps[k] = v
		}
	}
	nodeData.ConfigMaps = configMaps

	data.Node = nodeData

	return data, nil
}

func (r *ConfigSetReconciler) buildTemplate(ctx context.Context, log logr.Logger, template string, data Data) (content []byte, err error) {
	// echo '{"foo": {"foo": "bar"}}' | gomplate -i '{{(ds "data").foo.foo}}' -d data=stdin:///foo.json

	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	command := "gomplate"
	arg := []string{
		"-i",
		template,
		"-d",
		"data=stdin:///data.json",
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(command, arg...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	_, err = in.Write(b)
	if err != nil {
		return nil, err
	}

	err = in.Close()
	if err != nil {
		return nil, err
	}

	err = cmd.Run()
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to execute %q: %s", command, stderr.String()))
	}

	content = stdout.Bytes()

	return
}

// writeFileContent is responsible for ensuring a file on disk matches the desired state.
func (r *ConfigSetReconciler) writeFileContent(ctx context.Context, file commonv1.File, log logr.Logger, handler common.FileHandler) (err error, changed bool) {
	// Determine the sha of the content
	var b bytes.Buffer
	_, err = b.WriteString(file.Content)
	if err != nil {
		return err, changed
	}

	chsh := sha256.New()
	contentHash := fmt.Sprintf("%x", chsh.Sum(b.Bytes()))

	if _, err = os.Stat(file.Path); os.IsNotExist(err) {
		_, err = os.Create(file.Path)
		if err != nil {
			return errors.Wrap(err, "failed to create new file"), changed
		}
	}

	// Read the sha of the file
	fileBytes, err := os.ReadFile(file.Path)
	if err != nil {
		return errors.Wrap(err, "failed to read file"), changed
	}
	fhsh := sha256.New()
	fileHash := fmt.Sprintf("%x", fhsh.Sum(fileBytes))

	// Write only when necessary
	if contentHash != fileHash {
		log.Info(fmt.Sprintf("writing file %q", file.Path))
		err = handler.WriteContentFile(ctx, file.Path, []byte(file.Content))
		if err != nil {
			return err, changed
		}
		changed = true
	}

	err = handler.Chown(ctx, file.Path, file.Owner, file.Group)
	if err != nil {
		return errors.Wrap(err, "failed to chown file"), changed
	}

	err = handler.SetMode(ctx, file.Path, file.Mode)
	if err != nil {
		return errors.Wrap(err, "failed to set file mode"), changed
	}

	return nil, changed
}
