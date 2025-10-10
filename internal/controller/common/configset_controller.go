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

package common

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/pkg/errors"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/packages"
	"github.com/zachfi/nodemanager/pkg/services"
	"github.com/zachfi/nodemanager/pkg/services/systemd"
)

// ConfigSetReconciler reconciles a ConfigSet object
type ConfigSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	tracer trace.Tracer
	logger *slog.Logger
	system handler.System
	locker Locker
	cfg    ConfigSetConfig
}

func NewConfigSetReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg ConfigSetConfig, system handler.System, locker Locker) *ConfigSetReconciler {
	return &ConfigSetReconciler{
		Client: client,
		Scheme: scheme,
		tracer: otel.Tracer("controller.common.configset"),
		logger: logger.With("controller", "configset"),
		locker: locker,
		system: system,
		cfg:    cfg,
	}
}

//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=configsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=configsets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=configsets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to handle changes to a ConfigSet object.
func (r *ConfigSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error

	attributes := []attribute.KeyValue{
		attribute.String("req", req.String()),
		attribute.String("namespace", req.Namespace),
	}

	ctx, span := r.tracer.Start(ctx, "Reconcile", trace.WithAttributes(attributes...))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	var configSet commonv1.ConfigSet
	if err = r.Get(ctx, req.NamespacedName, &configSet); err != nil {
		r.logger.Error("failed to get resource", "err", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	node, err := createOrGetNode(ctx, r.logger, r, r, req)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = nodeLabelMatch(node, configSet.Labels)
	if err != nil {
		err = nil                 // for the span defer
		return ctrl.Result{}, nil // Don't error if the configset doesn't match our label set
	}

	err = r.handlePackageSet(ctx, configSet.Spec.Packages)
	if err != nil {
		return ctrl.Result{}, err
	}

	changedFiles, err := r.handleFileSet(ctx, req.Namespace, configSet.Spec.Files, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleServiceSet(ctx, configSet.Spec.Services, changedFiles)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleExecutions(ctx, configSet.Spec.Executions, changedFiles)
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

func (r *ConfigSetReconciler) handlePackageSet(ctx context.Context, packageSet []commonv1.Package) error {
	ctx, span := r.tracer.Start(ctx, "handlePackageSet")
	defer span.End()

	handler := r.system.Package()

	currentlyInstalled := func(packages []string, pkg string) bool {
		return slices.Contains(packages, pkg)
	}

	pkgs, err := handler.List(ctx)
	if err != nil {
		return err
	}

	for _, pkg := range packageSet {
		switch packages.PackageEnsureFromString(pkg.Ensure) {
		case packages.Installed:
			if !currentlyInstalled(pkgs, pkg.Name) {
				err := handler.Install(ctx, pkg.Name)
				if err != nil {
					return err
				}
			}
		case packages.Absent:
			if currentlyInstalled(pkgs, pkg.Name) {
				err := handler.Remove(ctx, pkg.Name)
				r.logger.Info("removing package", "name", pkg.Name)
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

func (r *ConfigSetReconciler) WithTracer(tracer trace.Tracer) {
	r.tracer = tracer
}

func (r *ConfigSetReconciler) WithLogger(logger *slog.Logger) {
	r.logger = logger
}

func (r *ConfigSetReconciler) WithSystem(system handler.System) {
	r.system = system
}

func (r *ConfigSetReconciler) handleServiceSet(ctx context.Context, serviceSet []commonv1.Service, changedFiles []string) error {
	ctx, span := r.tracer.Start(ctx, "handleServiceSet")
	defer span.End()

	handler := r.system.Service()

	var (
		totalErrs       error
		restartServices = make(map[string]context.Context)
	)

	for _, cf := range changedFiles {
		for _, svc := range serviceSet {
			svcCtx := serviceContext(ctx, svc.User)
			// Only record services for restart that are supposed to be running
			if svc.Ensure == services.Running.String() {
				for _, sub := range svc.SusbscribeFiles {
					if sub == cf {
						restartServices[svc.Name] = svcCtx
					}
				}
			}
		}
	}

	for _, svc := range serviceSet {
		svcCtx := serviceContext(ctx, svc.User)

		if svc.Enable {
			err := handler.Enable(svcCtx, svc.Name)
			if err != nil {
				return errors.Wrap(err, "failed to enable service")
			}
		} else {
			err := handler.Disable(svcCtx, svc.Name)
			if err != nil {
				return errors.Wrap(err, "failed to disable service")
			}
		}

		if svc.Arguments != "" {
			err := handler.SetArguments(svcCtx, svc.Name, svc.Arguments)
			if err != nil {
				return errors.Wrap(err, "failed to set service arguments")
			}
		}

		status, _ := handler.Status(svcCtx, svc.Name)
		span.SetAttributes(attribute.String("status", status.String()))

		switch services.ServiceStatusFromString(svc.Ensure) {
		case services.Running:
			if status != services.Running {
				err := handler.Start(svcCtx, svc.Name)
				if err != nil {
					return errors.Wrap(err, "failed to start service")
				}
			}
		case services.Stopped:
			if status != services.Stopped {
				err := handler.Stop(svcCtx, svc.Name)
				if err != nil {
					return errors.Wrap(err, "failed to stop service")
				}
			}
		}
	}

	for restart, svcCtx := range restartServices {
		err := handler.Restart(svcCtx, restart)
		r.logger.Info("restarting service", "name", restart)
		if err != nil {
			totalErrs = fmt.Errorf("%w: %s", totalErrs, err.Error())
		}
	}

	return totalErrs
}

func serviceContext(ctx context.Context, user string) context.Context {
	if user != "" {
		ctx = context.WithValue(ctx, systemd.UserContextKey, user)
	}
	return ctx
}

// handleFileSet
func (r *ConfigSetReconciler) handleFileSet(ctx context.Context, namespace string, fileSet []commonv1.File, node commonv1.ManagedNode) ([]string, error) {
	ctx, span := r.tracer.Start(ctx, "handleFileSet")
	defer span.End()

	handler := r.system.File()

	changedFiles := make([]string, 0, len(fileSet))

	for _, file := range fileSet {
		switch files.FileEnsureFromString(file.Ensure) {
		case files.File:
			// If we have a template, let's set the content based on the rendered template.
			if file.Template != "" {
				data, err := r.collectData(ctx, namespace, file, node)
				if err != nil {
					return changedFiles, err
				}

				content, err := r.buildTemplate(ctx, file.Template, data)
				if err != nil {
					return changedFiles, errors.Wrap(err, fmt.Sprintf("failed to build template for file %q: %s", file.Path, file.Template))
				}

				if len(content) > 0 {
					file.Content = string(content)
				}
			}

			if file.Content != "" && file.Ensure != files.Absent.String() {
				err, changed := r.writeFileContent(ctx, file, handler)
				if err != nil {
					return changedFiles, err
				}

				if changed {
					changedFiles = append(changedFiles, file.Path)
				}
			}

		case files.Directory:
			// Create the directory if it does not exist, with the correct mode.
			if _, err := os.Stat(file.Path); os.IsNotExist(err) {
				var fileMode os.FileMode

				if file.Mode == "" {
					fileMode = os.FileMode(0o660)
				} else {
					fileMode, err = files.GetFileModeFromString(ctx, file.Mode)
					if err != nil {
						return changedFiles, errors.Wrap(err, "failed to get file mode from string")
					}
				}

				err := os.Mkdir(file.Path, fileMode)
				if err != nil {
					return changedFiles, err
				}
				changedFiles = append(changedFiles, file.Path)
			} else {
				// Set the mode
				if file.Mode != "" {
					err := handler.SetMode(ctx, file.Path, file.Mode)
					if err != nil {
						return changedFiles, errors.Wrap(err, "failed to set file mode")
					}
				}
			}

		case files.Symlink:
			target, err := os.Readlink(file.Path)
			if err != nil {
				return changedFiles, errors.Wrapf(err, "failed to read symlink %q", file.Path)
			}

			if target != file.Target {
				r.logger.Info("symlinking file", "name", file.Path)

				if _, err := os.Lstat(file.Path); err == nil {
					os.Remove(file.Path)
				}

				err := os.Symlink(file.Target, file.Path)
				if err != nil {
					return changedFiles, errors.Wrapf(err, "failed to create symlink %q -> %q", file.Path, file.Target)
				}
			}
		case files.Absent:
			err := os.Remove(file.Path)
			if err != nil {
				return changedFiles, errors.Wrapf(err, "failed to remove file %q", file.Path)
			}
		default:
			return changedFiles, fmt.Errorf("unhandled file ensure %q", file.Ensure)
		}
	}

	return changedFiles, nil
}

func (r *ConfigSetReconciler) handleExecutions(ctx context.Context, serviceSet []commonv1.Exec, changedFiles []string) error {
	ctx, span := r.tracer.Start(ctx, "handleExecutions")
	defer span.End()

	handler := r.system.Exec()

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
		r.logger.Info("running exec", "command", exe.Command)
		if err != nil {
			totalErrs = fmt.Errorf("%w: %s", totalErrs, err.Error())
		}
	}

	return totalErrs
}

func (r *ConfigSetReconciler) collectData(ctx context.Context, namespace string, file commonv1.File, node commonv1.ManagedNode) (data Data, err error) {
	var nodeData NodeData
	nodeData.Labels = node.Labels

	secrets := map[string][]byte{}
	for _, s := range file.SecretRefs {

		// Render the secretRef in case it is a template string
		st, err := r.buildTemplate(ctx, s, Data{Node: nodeData})
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

func (r *ConfigSetReconciler) buildTemplate(ctx context.Context, template string, data Data) (content []byte, err error) {
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
func (r *ConfigSetReconciler) writeFileContent(ctx context.Context, file commonv1.File, handler handler.FileHandler) (err error, changed bool) {
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
		r.logger.Info("writing file", "path", file.Path)
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
