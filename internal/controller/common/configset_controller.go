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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

	// lastResourceVersion tracks the resource_version label most recently recorded
	// for each (node, configset) pair so stale label sets can be deleted from the
	// configSetAppliedResourceVersion gauge.
	lastResourceVersionMu sync.Mutex
	lastResourceVersion   map[string]string // key: "node/configset"
}

func NewConfigSetReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg ConfigSetConfig, system handler.System, locker locker.Locker) *ConfigSetReconciler {
	return &ConfigSetReconciler{
		Client:              client,
		Scheme:              scheme,
		tracer:              otel.Tracer("controller.common.configset"),
		logger:              logger.With("controller", "configset"),
		locker:              locker,
		system:              system,
		cfg:                 cfg,
		lastResourceVersion: make(map[string]string),
	}
}

//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=configsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=configsets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=configsets/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to handle changes to a ConfigSet object.
func (r *ConfigSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger.Debug("reconciling configset", "configset", req.Name)

	// Prevent a single stuck reconcile from blocking the worker indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var err error

	if ctx.Err() != nil {
		r.logger.Warn("context already cancelled, skipping reconcile", "configset", req.Name)
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
		if client.IgnoreNotFound(err) == nil {
			span.AddEvent("configset deleted, cleaning up status")
			r.logger.Debug("configset deleted, cleaning up status", "configset", req.Name)
			r.removeConfigSetStatus(ctx, req.Name)
		} else {
			r.logger.Error("failed to get resource", "err", err)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	span.SetAttributes(attribute.String("configset", configSet.Name))

	node, err := createOrGetNode(ctx, r.logger, r, r, req)
	if err != nil {
		r.logger.Error("failed to create or get managed node", "err", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	err = nodeLabelMatch(node, configSet.Labels)
	if err != nil {
		span.AddEvent("labels do not match, cleaning up status",
			trace.WithAttributes(attribute.String("node", node.Name)))
		r.logger.Debug("configset labels do not match node, skipping", "configset", configSet.Name, "node", node.Name)
		r.removeConfigSetStatus(ctx, configSet.Name)
		err = nil // for the span defer
		// Requeue so we retry after the ManagedNode reconciler sets labels.
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	nodeName := node.Name

	var conflicts []string
	conflicts, err = r.detectConflicts(ctx, &configSet, node)
	if err != nil {
		r.logger.Error("failed to detect conflicts", "configset", configSet.Name, "err", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	if len(conflicts) > 0 {
		span.AddEvent("resource conflicts detected",
			trace.WithAttributes(
				attribute.Int("count", len(conflicts)),
				attribute.StringSlice("conflicts", conflicts)))
		configSetConflictsTotal.WithLabelValues(nodeName, configSet.Name).Add(float64(len(conflicts)))
		r.logger.Warn("configset has resource conflicts, skipping apply", "configset", configSet.Name, "conflicts", conflicts)
		if statusErr := r.updateConfigSetStatus(ctx, node.Name, node.Namespace, configSet.Name, configSet.ResourceVersion, nil, conflicts); statusErr != nil {
			r.logger.Error("failed to update conflict status on node", "err", statusErr)
		}
		if statusErr := r.updateConfigSetCondition(ctx, req, conflicts); statusErr != nil {
			r.logger.Error("failed to update conflict condition on configset", "err", statusErr)
		}
		err = nil
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Clear any previously recorded conflict condition now that the conflict is resolved.
	if statusErr := r.updateConfigSetCondition(ctx, req, nil); statusErr != nil {
		r.logger.Error("failed to clear conflict condition on configset", "err", statusErr)
	}

	r.logger.Debug("applying configset", "configset", configSet.Name,
		"packages", len(configSet.Spec.Packages),
		"files", len(configSet.Spec.Files),
		"services", len(configSet.Spec.Services),
		"executions", len(configSet.Spec.Executions))

	applyStart := time.Now()

	var (
		changedFiles      []string
		pkgErr            error
		fileErr           error
		svcErr            error
		execErr           error
		fileBackupUpdates map[string]string
		phaseStart        time.Time
	)

	phaseStart = time.Now()
	pkgErr = r.handlePackageSet(ctx, nodeName, configSet.Spec.Packages)
	r.logger.Debug("packages handled", "configset", configSet.Name, "duration", time.Since(phaseStart), "err", pkgErr)

	phaseStart = time.Now()
	changedFiles, fileBackupUpdates, fileErr = r.handleFileSet(ctx, nodeName, configSet.Name, req.Namespace, configSet.Spec.Files, node)
	r.logger.Debug("files handled", "configset", configSet.Name, "duration", time.Since(phaseStart), "changed", len(changedFiles), "err", fileErr)

	phaseStart = time.Now()
	svcErr = r.handleServiceSet(ctx, nodeName, req.Namespace, configSet.Spec.Services, changedFiles)
	r.logger.Debug("services handled", "configset", configSet.Name, "duration", time.Since(phaseStart), "err", svcErr)

	phaseStart = time.Now()
	execErr = r.handleExecutions(ctx, configSet.Spec.Executions, changedFiles)
	r.logger.Debug("executions handled", "configset", configSet.Name, "duration", time.Since(phaseStart), "err", execErr)

	err = errors.Join(pkgErr, fileErr, svcErr, execErr)

	if len(fileBackupUpdates) > 0 {
		if backupErr := r.updateFileBackups(ctx, node.Name, node.Namespace, fileBackupUpdates); backupErr != nil {
			r.logger.Error("failed to update file backups on node", "err", backupErr)
		}
	}

	applyResult := "success"
	if err != nil {
		applyResult = "error"
	}
	span.SetAttributes(
		attribute.String("result", applyResult),
		attribute.String("resource_version", configSet.ResourceVersion))
	configSetApplyTotal.WithLabelValues(nodeName, configSet.Name, applyResult).Inc()
	configSetApplyDuration.WithLabelValues(nodeName, configSet.Name).Observe(time.Since(applyStart).Seconds())
	if err == nil {
		now := float64(time.Now().Unix())
		lastConfigSetApplyTimestamp.WithLabelValues(nodeName, configSet.Name).Set(now)
		r.recordResourceVersion(nodeName, configSet.Name, configSet.ResourceVersion, now)
	}

	if statusErr := r.updateConfigSetStatus(ctx, node.Name, node.Namespace, configSet.Name, configSet.ResourceVersion, err, nil); statusErr != nil {
		r.logger.Error("failed to update configset status on node", "err", statusErr)
	}

	if err != nil {
		// Use a fixed requeue instead of returning the error (which triggers
		// exponential backoff and can delay retries up to 15 minutes).
		r.logger.Error("configset apply failed, will retry", "configset", configSet.Name, "err", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	r.notifyResources(ctx, &configSet)

	return ctrl.Result{}, nil
}

// notifyResources touches a generation-scoped annotation on each resource
// listed in spec.notifies, triggering that resource's controller to reconcile.
// The annotation value encodes the ConfigSet name and generation so the touch
// is a no-op when the spec hasn't changed, preventing spurious reconcile loops.
func (r *ConfigSetReconciler) notifyResources(ctx context.Context, cs *commonv1.ConfigSet) {
	annotationKey := fmt.Sprintf("nodemanager.io/notify-%s", cs.Name)
	annotationValue := fmt.Sprintf("%d", cs.Generation)

	for _, ref := range cs.Spec.Notifies {
		ns := ref.Namespace
		if ns == "" {
			ns = cs.Namespace
		}

		if ref.Name != "" {
			r.touchNotifyTarget(ctx, ref.APIVersion, ref.Kind, ref.Name, ns, annotationKey, annotationValue)
			continue
		}

		// No name specified — notify all resources of this kind in the namespace.
		list := &unstructured.UnstructuredList{}
		list.SetAPIVersion(ref.APIVersion)
		list.SetKind(ref.Kind + "List")
		if err := r.List(ctx, list, client.InNamespace(ns)); err != nil {
			r.logger.Warn("failed to list resources for notification", "apiVersion", ref.APIVersion, "kind", ref.Kind, "err", err)
			continue
		}
		for _, item := range list.Items {
			r.touchNotifyTarget(ctx, ref.APIVersion, ref.Kind, item.GetName(), item.GetNamespace(), annotationKey, annotationValue)
		}
	}
}

func (r *ConfigSetReconciler) touchNotifyTarget(ctx context.Context, apiVersion, kind, name, namespace, annotationKey, annotationValue string) {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, u); err != nil {
		r.logger.Warn("failed to get notify target", "kind", kind, "name", name, "err", err)
		return
	}
	annotations := u.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	if annotations[annotationKey] == annotationValue {
		return // already notified for this generation
	}
	annotations[annotationKey] = annotationValue
	u.SetAnnotations(annotations)
	if err := r.Update(ctx, u); err != nil {
		r.logger.Warn("failed to notify target", "kind", kind, "name", name, "err", err)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.cfg.FileBucket.Enabled && r.cfg.FileBucket.MaxAge > 0 {
		if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
			r.runFileBucketGC(ctx)
			return nil
		})); err != nil {
			return fmt.Errorf("failed to register filebucket GC runnable: %w", err)
		}
	}

	hostname, err := r.system.Node().Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname for ManagedNode watch: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1.ConfigSet{}).
		Watches(&corev1.Secret{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.configSetsReferencingSecret)).
		Watches(&corev1.ConfigMap{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.configSetsReferencingConfigMap)).
		// Watch the local ManagedNode so label changes (e.g. role labels set after
		// startup) immediately re-trigger ConfigSet reconciliation without polling.
		// Use LabelChangedPredicate so status-only updates (SSH keys, interfaces,
		// WireGuard, configset apply results) don't cause a flood of reconciles.
		Watches(&commonv1.ManagedNode{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.configSetsOnNodeChange(hostname)),
			builder.WithPredicates(predicate.LabelChangedPredicate{})).
		Complete(r)
}

// runFileBucketGC runs the filebucket garbage collector on a durable schedule.
// It checks every hour whether the GC interval (MaxAge) has elapsed since the
// last run, consulting ManagedNode.Status.LastFileBucketGC so the interval is
// honoured across controller restarts and crash loops.
func (r *ConfigSetReconciler) runFileBucketGC(ctx context.Context) {
	maybeRunGC := func() {
		node, err := r.getLocalManagedNode(ctx)
		if err != nil {
			r.logger.Warn("filebucket GC: could not get local ManagedNode", "err", err)
			return
		}
		if node.Status.LastFileBucketGC != nil &&
			time.Since(node.Status.LastFileBucketGC.Time) < r.cfg.FileBucket.MaxAge {
			return
		}
		if gcErr := files.GCFileBucket(r.cfg.FileBucket.Path, r.cfg.FileBucket.MaxAge, r.logger); gcErr != nil {
			r.logger.Error("filebucket GC failed", "err", gcErr)
			return
		}
		r.updateLastGCTimestamp(ctx, node)
	}

	maybeRunGC()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			maybeRunGC()
		}
	}
}

// configSetsOnNodeChange returns a mapper that enqueues all ConfigSets in the
// controller namespace when the local ManagedNode changes. This ensures that
// label changes on the node (e.g. role labels applied after startup) are
// reflected in ConfigSet label-match evaluation without polling.
func (r *ConfigSetReconciler) configSetsOnNodeChange(hostname string) ctrlhandler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		if obj.GetName() != hostname {
			return nil
		}
		var list commonv1.ConfigSetList
		if err := r.List(ctx, &list, client.InNamespace(r.cfg.Namespace)); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, len(list.Items))
		for i, cs := range list.Items {
			reqs[i] = reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cs.Name, Namespace: cs.Namespace},
			}
		}
		return reqs
	}
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
				// Skip the write if nothing meaningful changed — avoids triggering
				// a ManagedNode watch event (and a downstream ManagedNode reconcile)
				// on every ConfigSet reconcile.
				if cs.ResourceVersion == entry.ResourceVersion &&
					cs.Error == entry.Error &&
					slicesEqual(cs.Conflicts, entry.Conflicts) {
					return nil
				}
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

// removeConfigSetStatus removes the ConfigSetApplyStatus entry for the named
// ConfigSet from the local ManagedNode and cleans up associated Prometheus
// metrics.  Called when a ConfigSet is deleted or its labels no longer match.
func (r *ConfigSetReconciler) removeConfigSetStatus(ctx context.Context, configSetName string) {
	node, err := r.getLocalManagedNode(ctx)
	if err != nil {
		r.logger.Debug("could not fetch local ManagedNode for status cleanup", "err", err)
		return
	}

	nodeName := node.Name

	// Remove from ManagedNode status.
	found := false
	filtered := make([]commonv1.ConfigSetApplyStatus, 0, len(node.Status.ConfigSets))
	for _, cs := range node.Status.ConfigSets {
		if cs.Name == configSetName {
			found = true
			continue
		}
		filtered = append(filtered, cs)
	}
	if found {
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			var n commonv1.ManagedNode
			if err := r.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, &n); err != nil {
				return err
			}
			updated := make([]commonv1.ConfigSetApplyStatus, 0, len(n.Status.ConfigSets))
			for _, cs := range n.Status.ConfigSets {
				if cs.Name != configSetName {
					updated = append(updated, cs)
				}
			}
			n.Status.ConfigSets = updated
			return r.Status().Update(ctx, &n)
		})
		if err != nil {
			r.logger.Error("failed to remove configset status from ManagedNode", "configset", configSetName, "err", err)
		} else {
			r.logger.Info("removed stale configset status", "configset", configSetName, "node", nodeName)
		}
	}

	// Clean up Prometheus metrics for this configset.
	lastConfigSetApplyTimestamp.DeleteLabelValues(nodeName, configSetName)
	configSetConflictsTotal.DeleteLabelValues(nodeName, configSetName)
	configSetApplyDuration.DeleteLabelValues(nodeName, configSetName)
	configSetApplyTotal.DeleteLabelValues(nodeName, configSetName, "success")
	configSetApplyTotal.DeleteLabelValues(nodeName, configSetName, "error")
	fileChangesTotal.DeleteLabelValues(nodeName, configSetName, "success")
	fileChangesTotal.DeleteLabelValues(nodeName, configSetName, "error")

	r.lastResourceVersionMu.Lock()
	key := nodeName + "/" + configSetName
	prev := r.lastResourceVersion[key]
	delete(r.lastResourceVersion, key)
	r.lastResourceVersionMu.Unlock()
	if prev != "" {
		configSetAppliedResourceVersion.DeleteLabelValues(nodeName, configSetName, prev)
	}
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

		existing := meta.FindStatusCondition(cs.Status.Conditions, condition.Type)
		if existing != nil &&
			existing.Status == condition.Status &&
			existing.Reason == condition.Reason &&
			existing.Message == condition.Message &&
			existing.ObservedGeneration == condition.ObservedGeneration {
			return nil
		}

		meta.SetStatusCondition(&cs.Status.Conditions, condition)
		return r.Status().Update(ctx, &cs)
	})
}

// detectConflicts returns a human-readable description of every file path or
// service name that is claimed by both cs and another ConfigSet matching this
// node. A non-empty result means cs must not be applied this reconcile cycle.
// slicesEqual compares two string slices, treating nil and empty as equivalent.
// This avoids spurious status writes from nil vs []string{} differences after
// Kubernetes JSON round-tripping.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

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
		svcHandler := withUserContext(handler, svcCtx)

		if svc.Enable {
			enableErr := svcHandler.Enable(svcCtx, svc.Name)
			result := "success"
			if enableErr != nil {
				result = "error"
				errs = append(errs, fmt.Errorf("failed to enable service %q: %w", svc.Name, enableErr))
			}
			serviceOperationsTotal.WithLabelValues(nodeName, "enable", result).Inc()
		} else {
			disableErr := svcHandler.Disable(svcCtx, svc.Name)
			result := "success"
			if disableErr != nil {
				result = "error"
				errs = append(errs, fmt.Errorf("failed to disable service %q: %w", svc.Name, disableErr))
			}
			serviceOperationsTotal.WithLabelValues(nodeName, "disable", result).Inc()
		}

		if svc.Arguments != "" {
			if argsErr := svcHandler.SetArguments(svcCtx, svc.Name, svc.Arguments); argsErr != nil {
				errs = append(errs, fmt.Errorf("failed to set service arguments for %q: %w", svc.Name, argsErr))
			}
		}

		status, _ := svcHandler.Status(svcCtx, svc.Name)
		span.SetAttributes(attribute.String("status", status.String()))

		switch services.ServiceStatusFromString(svc.Ensure) {
		case services.Running:
			if status != services.Running {
				startErr := svcHandler.Start(svcCtx, svc.Name)
				result := "success"
				if startErr != nil {
					result = "error"
					errs = append(errs, fmt.Errorf("failed to start service %q: %w", svc.Name, startErr))
				}
				serviceOperationsTotal.WithLabelValues(nodeName, "start", result).Inc()
			}
		case services.Stopped:
			if status != services.Stopped {
				stopErr := svcHandler.Stop(svcCtx, svc.Name)
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
		restartHandler := withUserContext(handler, restartSvc.Context)
		err = restartHandler.Restart(restartSvc.Context, restart)
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

// withUserContext returns a service handler that is aware of the user set on
// the context. For systemd, this causes systemctl to use --user -M <user>@.
func withUserContext(h handler.ServiceHandler, ctx context.Context) handler.ServiceHandler {
	if s, ok := h.(*systemd.Systemd); ok {
		return s.WithContext(ctx)
	}
	return h
}

// managedPathsUnder returns the set of file paths declared by all ConfigSets
// that match node across all configsets in namespace, filtered to those whose
// cleaned path starts with dirPath.
func (r *ConfigSetReconciler) managedPathsUnder(ctx context.Context, namespace string, node commonv1.ManagedNode, dirPath string) map[string]struct{} {
	managed := make(map[string]struct{})
	var list commonv1.ConfigSetList
	if err := r.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		r.logger.Warn("purge: failed to list configsets", "err", err)
		return managed
	}
	prefix := dirPath
	if prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	for _, cs := range list.Items {
		if nodeLabelMatch(node, cs.Labels) != nil {
			continue // this configset does not apply to this node
		}
		for _, f := range cs.Spec.Files {
			if f.Path == dirPath || strings.HasPrefix(f.Path, prefix) {
				managed[f.Path] = struct{}{}
			}
		}
	}
	return managed
}

// handleFileSet
func (r *ConfigSetReconciler) handleFileSet(ctx context.Context, nodeName string, configSetName string, namespace string, fileSet []commonv1.File, node commonv1.ManagedNode) ([]string, map[string]string, error) {
	ctx, span := r.tracer.Start(ctx, "handleFileSet")
	defer span.End()

	handler := r.system.File()

	changedFiles := make([]string, 0, len(fileSet))
	fileBackupUpdates := make(map[string]string)
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
				if file.CreateOnly {
					if _, statErr := os.Stat(file.Path); statErr == nil {
						continue // file exists; leave it untouched
					}
				}

				changed, backupHash, writeErr := r.writeFileContent(ctx, file, handler)
				if writeErr != nil {
					errs = append(errs, writeErr)
					continue
				}
				if changed {
					changedFiles = append(changedFiles, file.Path)
				}
				if backupHash != "" {
					fileBackupUpdates[file.Path] = backupHash
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

			if file.Purge {
				managed := r.managedPathsUnder(ctx, namespace, node, file.Path)
				dirEntries, readErr := os.ReadDir(file.Path)
				if readErr != nil {
					errs = append(errs, fmt.Errorf("purge: failed to read directory %q: %w", file.Path, readErr))
					continue
				}
				for _, entry := range dirEntries {
					if entry.IsDir() {
						continue // never remove subdirectories
					}
					entryPath := file.Path + "/" + entry.Name()
					if _, ok := managed[entryPath]; !ok {
						r.logger.Info("purging unmanaged file", "path", entryPath, "directory", file.Path)
						if removeErr := os.Remove(entryPath); removeErr != nil {
							errs = append(errs, fmt.Errorf("purge: failed to remove %q: %w", entryPath, removeErr))
						} else {
							changedFiles = append(changedFiles, entryPath)
						}
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

	return changedFiles, fileBackupUpdates, errors.Join(errs...)
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

	cmd := exec.CommandContext(ctx, command, arg...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewReader(b)
	cmd.WaitDelay = 10 * time.Second

	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to execute %q: %s: %w", command, stderr.String(), err)
	}

	content = stdout.Bytes()

	return content, err
}

// updateFileBackups merges new path→backupHash entries into ManagedNode.Status.FileBackups.
func (r *ConfigSetReconciler) updateFileBackups(ctx context.Context, nodeName, nodeNamespace string, updates map[string]string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var node commonv1.ManagedNode
		if err := r.Get(ctx, types.NamespacedName{Name: nodeName, Namespace: nodeNamespace}, &node); err != nil {
			return err
		}

		if node.Status.FileBackups == nil {
			node.Status.FileBackups = make(map[string]string, len(updates))
		}
		for path, hash := range updates {
			node.Status.FileBackups[path] = hash
		}

		return r.Status().Update(ctx, &node)
	})
}

// getLocalManagedNode fetches the ManagedNode for the local hostname.
func (r *ConfigSetReconciler) getLocalManagedNode(ctx context.Context) (commonv1.ManagedNode, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return commonv1.ManagedNode{}, err
	}

	var node commonv1.ManagedNode
	err = r.Get(ctx, types.NamespacedName{Name: hostname, Namespace: r.cfg.Namespace}, &node)
	return node, err
}

// updateLastGCTimestamp records the current time as LastFileBucketGC in the node status.
func (r *ConfigSetReconciler) updateLastGCTimestamp(ctx context.Context, node commonv1.ManagedNode) {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var n commonv1.ManagedNode
		if err := r.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, &n); err != nil {
			return err
		}
		now := metav1.Now()
		n.Status.LastFileBucketGC = &now
		return r.Status().Update(ctx, &n)
	})
	if err != nil {
		r.logger.Error("failed to update LastFileBucketGC on node", "err", err)
	}
}

// writeFileContent is responsible for ensuring a file on disk matches the desired state.
// It returns whether anything changed, the SHA256 hash of any pre-write backup (empty
// string if no backup was taken), and any error.
func (r *ConfigSetReconciler) writeFileContent(ctx context.Context, file commonv1.File, handler handler.FileHandler) (changed bool, backupHash string, err error) {
	// Filebucket: back up the existing file before overwriting it.
	if r.cfg.FileBucket.Enabled {
		info, statErr := os.Stat(file.Path)
		if statErr == nil && !info.IsDir() {
			if r.cfg.FileBucket.MaxFileSizeBytes > 0 && info.Size() > r.cfg.FileBucket.MaxFileSizeBytes {
				r.logger.Warn("skipping filebucket backup: file too large",
					"path", file.Path,
					"size", info.Size(),
					"limit", r.cfg.FileBucket.MaxFileSizeBytes,
				)
			} else {
				current, readErr := os.ReadFile(file.Path)
				if readErr == nil {
					h, bucketErr := files.SaveToFileBucket(r.cfg.FileBucket.Path, file.Path, current, info)
					if bucketErr != nil {
						r.logger.Warn("filebucket backup failed", "path", file.Path, "err", bucketErr)
					} else {
						backupHash = h
						r.logger.Info("backed up file to filebucket",
							"path", file.Path,
							"hash", h,
							"bucket", r.cfg.FileBucket.Path,
						)
					}
				}
			}
		}
	}

	var contentChanged, ownerChanged, modeChanged bool

	contentChanged, err = handler.WriteContentFile(ctx, file.Path, []byte(file.Content))
	if err != nil {
		return false, backupHash, fmt.Errorf("failed to write content to file: %w", err)
	}

	ownerChanged, err = handler.Chown(ctx, file.Path, file.Owner, file.Group)
	if err != nil {
		return true, backupHash, fmt.Errorf("failed to chown file: %w", err)
	}

	if file.Mode != "" {
		modeChanged, err = handler.SetMode(ctx, file.Path, file.Mode)
		if err != nil {
			return true, backupHash, fmt.Errorf("failed to set file mode: %w", err)
		}
	}

	return contentChanged || ownerChanged || modeChanged, backupHash, nil
}

// recordResourceVersion sets configSetAppliedResourceVersion for the given
// (node, configset, resource_version) and removes the gauge entry for any
// previous resource_version so cardinality stays bounded.
func (r *ConfigSetReconciler) recordResourceVersion(node, configset, resourceVersion string, ts float64) {
	key := node + "/" + configset
	r.lastResourceVersionMu.Lock()
	if r.lastResourceVersion == nil {
		r.lastResourceVersion = make(map[string]string)
	}
	prev := r.lastResourceVersion[key]
	r.lastResourceVersion[key] = resourceVersion
	r.lastResourceVersionMu.Unlock()

	if prev != "" && prev != resourceVersion {
		configSetAppliedResourceVersion.DeleteLabelValues(node, configset, prev)
	}
	configSetAppliedResourceVersion.WithLabelValues(node, configset, resourceVersion).Set(ts)
}
