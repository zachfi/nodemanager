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
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"slices"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/locker"
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
	locker locker.Locker
	cfg    ConfigSetConfig
}

func NewConfigSetReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg ConfigSetConfig, system handler.System, locker locker.Locker) *ConfigSetReconciler {
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
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to handle changes to a ConfigSet object.
func (r *ConfigSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error

	if ctx.Err() != nil {
		return ctrl.Result{}, nil
	}

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

	nodeName := node.Name

	var conflicts []string
	conflicts, err = r.detectConflicts(ctx, &configSet, node)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(conflicts) > 0 {
		configSetConflictsTotal.WithLabelValues(nodeName, configSet.Name).Add(float64(len(conflicts)))
		r.logger.Warn("configset has resource conflicts, skipping apply", "configset", configSet.Name, "conflicts", conflicts)
		if statusErr := r.updateConfigSetStatus(ctx, node.Name, node.Namespace, configSet.Name, configSet.ResourceVersion, nil, conflicts); statusErr != nil {
			r.logger.Error("failed to update conflict status on node", "err", statusErr)
		}
		if statusErr := r.updateConfigSetCondition(ctx, req, conflicts); statusErr != nil {
			r.logger.Error("failed to update conflict condition on configset", "err", statusErr)
		}
		err = nil
		return ctrl.Result{}, nil
	}

	// Clear any previously recorded conflict condition now that the conflict is resolved.
	if statusErr := r.updateConfigSetCondition(ctx, req, nil); statusErr != nil {
		r.logger.Error("failed to clear conflict condition on configset", "err", statusErr)
	}

	applyStart := time.Now()

	var (
		changedFiles []string
		pkgErr       error
		fileErr      error
		svcErr       error
		execErr      error
		fileHashUpdates map[string]string
	)

	pkgErr = r.handlePackageSet(ctx, nodeName, configSet.Spec.Packages)
	changedFiles, fileHashUpdates, fileErr = r.handleFileSet(ctx, nodeName, configSet.Name, req.Namespace, configSet.Spec.Files, node)
	svcErr = r.handleServiceSet(ctx, nodeName, req.Namespace, configSet.Spec.Services, changedFiles)
	execErr = r.handleExecutions(ctx, configSet.Spec.Executions, changedFiles)
	err = errors.Join(pkgErr, fileErr, svcErr, execErr)

	if len(fileHashUpdates) > 0 {
		if hashErr := r.updateFileHashes(ctx, node.Name, node.Namespace, fileHashUpdates); hashErr != nil {
			r.logger.Error("failed to update file hashes on node", "err", hashErr)
		}
	}

	applyResult := "success"
	if err != nil {
		applyResult = "error"
	}
	configSetApplyTotal.WithLabelValues(nodeName, configSet.Name, applyResult).Inc()
	configSetApplyDuration.WithLabelValues(nodeName, configSet.Name).Observe(time.Since(applyStart).Seconds())
	if err == nil {
		lastConfigSetApplyTimestamp.WithLabelValues(nodeName, configSet.Name).Set(float64(time.Now().Unix()))
	}

	if statusErr := r.updateConfigSetStatus(ctx, node.Name, node.Namespace, configSet.Name, configSet.ResourceVersion, err, nil); statusErr != nil {
		r.logger.Error("failed to update configset status on node", "err", statusErr)
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1.ConfigSet{}).
		Watches(&corev1.Secret{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.configSetsReferencingSecret)).
		Watches(&corev1.ConfigMap{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.configSetsReferencingConfigMap)).
		Complete(r)
}

// configSetsReferencingSecret returns reconcile requests for every ConfigSet
// in the same namespace that references the changed Secret by name.
func (r *ConfigSetReconciler) configSetsReferencingSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	var list commonv1.ConfigSetList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for _, cs := range list.Items {
		for _, f := range cs.Spec.Files {
			if slices.Contains(f.SecretRefs, obj.GetName()) {
				reqs = append(reqs, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: cs.Name, Namespace: cs.Namespace},
				})
				break
			}
		}
	}
	return reqs
}

// configSetsReferencingConfigMap returns reconcile requests for every ConfigSet
// in the same namespace that references the changed ConfigMap by name.
func (r *ConfigSetReconciler) configSetsReferencingConfigMap(ctx context.Context, obj client.Object) []reconcile.Request {
	var list commonv1.ConfigSetList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for _, cs := range list.Items {
		for _, f := range cs.Spec.Files {
			if slices.Contains(f.ConfigMapRefs, obj.GetName()) {
				reqs = append(reqs, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: cs.Name, Namespace: cs.Namespace},
				})
				break
			}
		}
	}
	return reqs
}

// updateConfigSetStatus records the result of a ConfigSet reconciliation in the ManagedNode status.
// conflicts is non-nil when a conflict was detected; applyErr is non-nil when apply itself failed.
func (r *ConfigSetReconciler) updateConfigSetStatus(ctx context.Context, nodeName, nodeNamespace, configSetName, resourceVersion string, applyErr error, conflicts []string) error {
	entry := commonv1.ConfigSetApplyStatus{
		Name:            configSetName,
		ResourceVersion: resourceVersion,
		LastApplied:     metav1.Now(),
		Conflicts:       conflicts,
	}
	if applyErr != nil {
		entry.Error = applyErr.Error()
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var node commonv1.ManagedNode
		if err := r.Get(ctx, types.NamespacedName{Name: nodeName, Namespace: nodeNamespace}, &node); err != nil {
			return err
		}

		found := false
		for i, cs := range node.Status.ConfigSets {
			if cs.Name == configSetName {
				node.Status.ConfigSets[i] = entry
				found = true
				break
			}
		}
		if !found {
			node.Status.ConfigSets = append(node.Status.ConfigSets, entry)
		}

		return r.Status().Update(ctx, &node)
	})
}

// updateConfigSetCondition sets or clears the Conflicted condition on the ConfigSet itself.
// Pass a non-nil conflicts slice to set the condition; pass nil to clear it.
func (r *ConfigSetReconciler) updateConfigSetCondition(ctx context.Context, req ctrl.Request, conflicts []string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cs commonv1.ConfigSet
		if err := r.Get(ctx, req.NamespacedName, &cs); err != nil {
			return err
		}

		var condition metav1.Condition
		if len(conflicts) > 0 {
			condition = metav1.Condition{
				Type:               "Conflicted",
				Status:             metav1.ConditionTrue,
				Reason:             "ResourceConflict",
				Message:            fmt.Sprintf("resource overlap with another ConfigSet: %s", conflicts),
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: cs.Generation,
			}
		} else {
			condition = metav1.Condition{
				Type:               "Conflicted",
				Status:             metav1.ConditionFalse,
				Reason:             "NoConflict",
				Message:            "",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: cs.Generation,
			}
		}

		// Update or append the condition.
		found := false
		for i, c := range cs.Status.Conditions {
			if c.Type == condition.Type {
				// Only update LastTransitionTime if Status actually changed.
				if c.Status == condition.Status {
					condition.LastTransitionTime = c.LastTransitionTime
				}
				cs.Status.Conditions[i] = condition
				found = true
				break
			}
		}
		if !found {
			cs.Status.Conditions = append(cs.Status.Conditions, condition)
		}

		return r.Status().Update(ctx, &cs)
	})
}

// detectConflicts returns a human-readable description of every file path or
// service name that is claimed by both cs and another ConfigSet matching this
// node. A non-empty result means cs must not be applied this reconcile cycle.
func (r *ConfigSetReconciler) detectConflicts(ctx context.Context, cs *commonv1.ConfigSet, node commonv1.ManagedNode) ([]string, error) {
	var all commonv1.ConfigSetList
	if err := r.List(ctx, &all, client.InNamespace(cs.Namespace)); err != nil {
		return nil, err
	}

	// Build resource inventory from all OTHER ConfigSets that match this node.
	claimedFiles := make(map[string]string)    // path → owning configset name
	claimedServices := make(map[string]string) // name → owning configset name
	claimedPackages := make(map[string]string) // name → owning configset name

	for _, other := range all.Items {
		if other.Name == cs.Name {
			continue
		}
		if nodeLabelMatch(node, other.Labels) != nil {
			continue // doesn't apply to this node
		}
		for _, f := range other.Spec.Files {
			claimedFiles[f.Path] = other.Name
		}
		for _, s := range other.Spec.Services {
			claimedServices[s.Name] = other.Name
		}
		for _, p := range other.Spec.Packages {
			claimedPackages[p.Name] = other.Name
		}
	}

	var conflicts []string
	for _, f := range cs.Spec.Files {
		if owner, ok := claimedFiles[f.Path]; ok {
			conflicts = append(conflicts, fmt.Sprintf("file:%s (also in configset %q)", f.Path, owner))
		}
	}
	for _, s := range cs.Spec.Services {
		if owner, ok := claimedServices[s.Name]; ok {
			conflicts = append(conflicts, fmt.Sprintf("service:%s (also in configset %q)", s.Name, owner))
		}
	}
	for _, p := range cs.Spec.Packages {
		if owner, ok := claimedPackages[p.Name]; ok {
			conflicts = append(conflicts, fmt.Sprintf("package:%s (also in configset %q)", p.Name, owner))
		}
	}

	return conflicts, nil
}

func (r *ConfigSetReconciler) handlePackageSet(ctx context.Context, nodeName string, packageSet []commonv1.Package) error {
	ctx, span := r.tracer.Start(ctx, "handlePackageSet")
	defer span.End()

	handler := r.system.Package()

	pkgs, err := handler.List(ctx)
	if err != nil {
		return err
	}

	var errs []error
	for _, pkg := range packageSet {
		switch packages.PackageEnsureFromString(pkg.Ensure) {
		case packages.Installed:
			installedVersion, installed := pkgs[pkg.Name]
			needsInstall := !installed || (pkg.Version != "" && installedVersion != pkg.Version)
			if needsInstall {
				installErr := handler.Install(ctx, pkg.Name, pkg.Version)
				result := "success"
				if installErr != nil {
					result = "error"
					errs = append(errs, installErr)
				}
				packageOperationsTotal.WithLabelValues(nodeName, "install", result).Inc()
			}
		case packages.Absent:
			if _, installed := pkgs[pkg.Name]; installed {
				r.logger.Info("removing package", "name", pkg.Name)
				removeErr := handler.Remove(ctx, pkg.Name)
				result := "success"
				if removeErr != nil {
					result = "error"
					errs = append(errs, removeErr)
				}
				packageOperationsTotal.WithLabelValues(nodeName, "remove", result).Inc()
			}
		default:
			errs = append(errs, fmt.Errorf("unhandled Ensure value %q for package %q", pkg.Ensure, pkg.Name))
		}
	}

	return errors.Join(errs...)
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

func (r *ConfigSetReconciler) handleServiceSet(ctx context.Context, nodeName string, namespace string, serviceSet []commonv1.Service, changedFiles []string) error {
	ctx, span := r.tracer.Start(ctx, "handleServiceSet")
	defer span.End()

	handler := r.system.Service()

	type restartService struct {
		// Context is used to pass a user value on the systemd handler.
		context.Context
		commonv1.Service
	}

	var (
		errs            []error
		restartServices = make(map[string]restartService)
	)

	for _, cf := range changedFiles {
		for _, svc := range serviceSet {
			svcCtx := serviceContext(ctx, svc.User)
			// Only record services for restart that are supposed to be running
			if svc.Ensure == services.Running.String() {
				for _, sub := range svc.SusbscribeFiles {
					if sub == cf {
						r.logger.Debug("changed file will notify service", "file", cf, "svc", svc, "sub", sub)
						restartServices[svc.Name] = restartService{svcCtx, svc}
					}
				}
			}
		}
	}

	for _, svc := range serviceSet {
		svcCtx := serviceContext(ctx, svc.User)

		if svc.Enable {
			enableErr := handler.Enable(svcCtx, svc.Name)
			result := "success"
			if enableErr != nil {
				result = "error"
				errs = append(errs, fmt.Errorf("failed to enable service %q: %w", svc.Name, enableErr))
			}
			serviceOperationsTotal.WithLabelValues(nodeName, "enable", result).Inc()
		} else {
			disableErr := handler.Disable(svcCtx, svc.Name)
			result := "success"
			if disableErr != nil {
				result = "error"
				errs = append(errs, fmt.Errorf("failed to disable service %q: %w", svc.Name, disableErr))
			}
			serviceOperationsTotal.WithLabelValues(nodeName, "disable", result).Inc()
		}

		if svc.Arguments != "" {
			if argsErr := handler.SetArguments(svcCtx, svc.Name, svc.Arguments); argsErr != nil {
				errs = append(errs, fmt.Errorf("failed to set service arguments for %q: %w", svc.Name, argsErr))
			}
		}

		status, _ := handler.Status(svcCtx, svc.Name)
		span.SetAttributes(attribute.String("status", status.String()))

		switch services.ServiceStatusFromString(svc.Ensure) {
		case services.Running:
			if status != services.Running {
				startErr := handler.Start(svcCtx, svc.Name)
				result := "success"
				if startErr != nil {
					result = "error"
					errs = append(errs, fmt.Errorf("failed to start service %q: %w", svc.Name, startErr))
				}
				serviceOperationsTotal.WithLabelValues(nodeName, "start", result).Inc()
			}
		case services.Stopped:
			if status != services.Stopped {
				stopErr := handler.Stop(svcCtx, svc.Name)
				result := "success"
				if stopErr != nil {
					result = "error"
					errs = append(errs, fmt.Errorf("failed to stop service %q: %w", svc.Name, stopErr))
				}
				serviceOperationsTotal.WithLabelValues(nodeName, "stop", result).Inc()
			}
		}
	}

	var (
		req types.NamespacedName
		err error
	)

	restartF := func(restart string, restartSvc restartService) error {
		if restartSvc.LockGroup != "" {
			req = types.NamespacedName{
				Namespace: namespace,
				Name:      restartSvc.LockGroup,
			}

			if err = r.locker.Lock(ctx, req); err != nil {
				return fmt.Errorf("failed to acquire lock: %w", err)
			}

			defer func() {
				unlockErr := r.locker.Unlock(ctx, req)
				if unlockErr != nil {
					r.logger.Error("failed to unlock", "err", err)
				}
			}()
		}

		r.logger.Info("restarting service", "name", restart)
		err = handler.Restart(restartSvc.Context, restart)
		result := "success"
		if err != nil {
			result = "error"
		}
		serviceOperationsTotal.WithLabelValues(nodeName, "restart", result).Inc()
		if err != nil {
			return fmt.Errorf("failed to restart service %q: %w", restart, err)
		}

		return nil
	}

	for restart, restartSvc := range restartServices {
		err = restartF(restart, restartSvc)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func serviceContext(ctx context.Context, user string) context.Context {
	if user != "" {
		ctx = context.WithValue(ctx, systemd.UserContextKey, user)
	}
	return ctx
}

// handleFileSet
func (r *ConfigSetReconciler) handleFileSet(ctx context.Context, nodeName string, configSetName string, namespace string, fileSet []commonv1.File, node commonv1.ManagedNode) ([]string, map[string]string, error) {
	ctx, span := r.tracer.Start(ctx, "handleFileSet")
	defer span.End()

	handler := r.system.File()

	changedFiles := make([]string, 0, len(fileSet))
	fileHashUpdates := make(map[string]string)
	var errs []error

	for _, file := range fileSet {
		switch files.FileEnsureFromString(file.Ensure) {
		case files.File:
			// If we have a template, let's set the content based on the rendered template.
			if file.Template != "" {
				data, dataErr := r.collectData(ctx, namespace, file, node)
				if dataErr != nil {
					errs = append(errs, dataErr)
					continue
				}

				content, tmplErr := r.buildTemplate(ctx, file.Template, data)
				if tmplErr != nil {
					errs = append(errs, fmt.Errorf("failed to build template for file %q: %s: %w", file.Path, file.Template, tmplErr))
					continue
				}

				if len(content) > 0 {
					file.Content = string(content)
				}
			}

			if file.Content != "" && file.Ensure != files.Absent.String() {
				if file.ProtectContent {
					skip, skipErr := r.shouldSkipProtectedFile(file.Path, node.Status.FileHashes)
					if skipErr != nil {
						errs = append(errs, fmt.Errorf("failed to check protected file %q: %w", file.Path, skipErr))
						continue
					}
					if skip {
						r.logger.Info("skipping protected file modified out-of-band", "path", file.Path)
						continue
					}
				}

				changed, writeErr := r.writeFileContent(ctx, file, handler)
				if writeErr != nil {
					errs = append(errs, writeErr)
					continue
				}
				if changed {
					changedFiles = append(changedFiles, file.Path)
					if file.ProtectContent {
						fileHashUpdates[file.Path] = fileContentHash([]byte(file.Content))
					}
				}
			}

		case files.Directory:
			// Create the directory if it does not exist, with the correct mode.
			if _, statErr := os.Stat(file.Path); os.IsNotExist(statErr) {
				var fileMode os.FileMode

				if file.Mode == "" {
					fileMode = os.FileMode(0o660)
				} else {
					var modeErr error
					fileMode, modeErr = files.GetFileModeFromString(ctx, file.Mode)
					if modeErr != nil {
						errs = append(errs, fmt.Errorf("failed to get file mode from string: %w", modeErr))
						continue
					}
				}

				if mkdirErr := os.Mkdir(file.Path, fileMode); mkdirErr != nil {
					errs = append(errs, mkdirErr)
					continue
				}
				changedFiles = append(changedFiles, file.Path)
			} else {
				// Set the mode
				if file.Mode != "" {
					changed, modeErr := handler.SetMode(ctx, file.Path, file.Mode)
					if modeErr != nil {
						errs = append(errs, fmt.Errorf("failed to set file mode: %w", modeErr))
						continue
					}
					if changed {
						changedFiles = append(changedFiles, file.Path)
					}
				}
			}

		case files.Symlink:
			target, linkErr := os.Readlink(file.Path)
			if linkErr != nil {
				errs = append(errs, fmt.Errorf("failed to read symlink %q: %w", file.Path, linkErr))
				continue
			}

			if target != file.Target {
				changed, removeErr := handler.Remove(ctx, file.Path)
				if removeErr != nil {
					r.logger.Error("failed removing existing link", "path", file.Path, "err", removeErr)
					errs = append(errs, removeErr)
					continue
				}
				if changed {
					changedFiles = append(changedFiles, file.Path)
				}

				r.logger.Info("symlinking file", "name", file.Path)

				if symlinkErr := os.Symlink(file.Target, file.Path); symlinkErr != nil {
					errs = append(errs, fmt.Errorf("failed to create symlink %q -> %q: %w", file.Path, file.Target, symlinkErr))
					continue
				}
			}
		case files.Absent:
			changed, removeErr := handler.Remove(ctx, file.Path)
			if removeErr != nil {
				r.logger.Error("failed removing file", "path", file.Path, "err", removeErr)
				errs = append(errs, removeErr)
			}
			if changed {
				changedFiles = append(changedFiles, file.Path)
			}
		default:
			errs = append(errs, fmt.Errorf("unhandled file ensure %q", file.Ensure))
		}
	}

	fileChangesTotal.WithLabelValues(nodeName, configSetName, "success").Add(float64(len(changedFiles)))

	return changedFiles, fileHashUpdates, errors.Join(errs...)
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
	nodeData.Status = node.Status

	secrets := map[string][]byte{}
	for _, s := range file.SecretRefs {

		// Render the secretRef in case it is a template string
		st, err := r.buildTemplate(ctx, s, Data{Node: nodeData})
		if err != nil {
			return Data{}, fmt.Errorf("failed to build template string rendering secretRef: %w", err)
		}

		var secret corev1.Secret
		nsn := types.NamespacedName{
			Name:      string(st),
			Namespace: namespace,
		}
		if err := r.Get(ctx, nsn, &secret); err != nil {
			return Data{}, err
		}

		maps.Copy(secrets, secret.Data)
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

		maps.Copy(configMaps, configMap.Data)
	}
	nodeData.ConfigMaps = configMaps

	data.Node = nodeData

	// Collect all ManagedNodes so templates can generate per-peer config.
	var allNodes commonv1.ManagedNodeList
	if err := r.List(ctx, &allNodes, client.InNamespace(namespace)); err != nil {
		return Data{}, fmt.Errorf("listing ManagedNodes: %w", err)
	}
	for _, n := range allNodes.Items {
		data.Nodes = append(data.Nodes, NodeInfo{
			Name:   n.Name,
			Labels: n.Labels,
			Status: n.Status,
		})
	}
	slices.SortFunc(data.Nodes, func(a, b NodeInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})

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
		return nil, fmt.Errorf("failed to execute %q: %s: %w", command, stderr.String(), err)
	}

	content = stdout.Bytes()

	return content, err
}

// shouldSkipProtectedFile returns true when a file with ProtectContent enabled has been
// modified out-of-band since nodemanager last wrote it.  It reads the current on-disk
// content and compares its SHA256 hash to the stored last-written hash.
// Returns false (do not skip) when the file does not yet exist or has not been tracked.
func (r *ConfigSetReconciler) shouldSkipProtectedFile(path string, fileHashes map[string]string) (bool, error) {
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil // file doesn't exist yet; write it
	}
	if err != nil {
		return false, err
	}

	lastHash, tracked := fileHashes[path]
	if !tracked {
		return false, nil // never written by nodemanager; write it now and start tracking
	}

	return fileContentHash(current) != lastHash, nil
}

// updateFileHashes merges new path→hash entries into ManagedNode.Status.FileHashes.
func (r *ConfigSetReconciler) updateFileHashes(ctx context.Context, nodeName, nodeNamespace string, updates map[string]string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var node commonv1.ManagedNode
		if err := r.Get(ctx, types.NamespacedName{Name: nodeName, Namespace: nodeNamespace}, &node); err != nil {
			return err
		}

		if node.Status.FileHashes == nil {
			node.Status.FileHashes = make(map[string]string, len(updates))
		}
		for path, hash := range updates {
			node.Status.FileHashes[path] = hash
		}

		return r.Status().Update(ctx, &node)
	})
}

// fileContentHash returns the hex-encoded SHA256 hash of data.
func fileContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// writeFileContent is responsible for ensuring a file on disk matches the desired state.
func (r *ConfigSetReconciler) writeFileContent(ctx context.Context, file commonv1.File, handler handler.FileHandler) (bool, error) {
	var err error

	// Determine the sha of the content
	var b bytes.Buffer
	_, err = b.WriteString(file.Content)
	if err != nil {
		return false, err
	}

	var contentChanged, ownerChanged, modeChanged bool

	contentChanged, err = handler.WriteContentFile(ctx, file.Path, []byte(file.Content))
	if err != nil {
		return false, fmt.Errorf("failed to write content to file: %w", err)
	}

	ownerChanged, err = handler.Chown(ctx, file.Path, file.Owner, file.Group)
	if err != nil {
		return true, fmt.Errorf("failed to chown file: %w", err)
	}

	if file.Mode != "" {
		modeChanged, err = handler.SetMode(ctx, file.Path, file.Mode)
		if err != nil {
			return true, fmt.Errorf("failed to set file mode: %w", err)
		}
	}

	return contentChanged || ownerChanged || modeChanged, nil
}
