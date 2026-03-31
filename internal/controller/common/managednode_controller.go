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
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gorhill/cronexpr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/common"
	"github.com/zachfi/nodemanager/pkg/common/labels"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/locker"
	"github.com/zachfi/nodemanager/pkg/util"
)

// ManagedNodeReconciler reconciles a ManagedNode object
type ManagedNodeReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	tracer    trace.Tracer
	logger    *slog.Logger
	system    handler.System
	locker    locker.Locker
	cfg       ManagedNodeConfig
	clientset kubernetes.Interface
}

func NewManagedNodeReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg ManagedNodeConfig, system handler.System, locker locker.Locker, clientset kubernetes.Interface) *ManagedNodeReconciler {
	return &ManagedNodeReconciler{
		Client:    client,
		Scheme:    scheme,
		tracer:    otel.Tracer("controller.common.managednode"),
		logger:    logger.With("controller", "managednode"),
		locker:    locker,
		system:    system,
		cfg:       cfg,
		clientset: clientset,
	}
}

//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=common.nodemanager.nodemanager,resources=managednodes/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;patch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list
//+kubebuilder:rbac:groups="policy",resources=pods/eviction,verbs=create
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;create

// Reconcile is part of the main kubernetes reconciliation loop which aims to keep the k8s resource in sync with the current state of the node.
func (r *ManagedNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		err  error
		next time.Time
		node *commonv1.ManagedNode
	)

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

	node, err = util.GetNode(ctx, r, req, r.system.Node())
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if node == nil {
		r.logger.Info("node not found, skipping reconciliation", "req", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	if err = r.maybeUncordon(ctx, node); err != nil {
		return ctrl.Result{}, err
	}

	err = r.updateNodeLabels(ctx, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.updateNodeStatus(ctx, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	next, err = r.handleUpgrade(ctx, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !next.IsZero() {
		return ctrl.Result{RequeueAfter: time.Until(next)}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	hostname, err := r.system.Node().Hostname()
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1.ManagedNode{}, builder.WithPredicates(newNameFilterPredicate(hostname))).
		Complete(r)
}

func newNameFilterPredicate(targetName string) predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetName() == targetName
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.GetName() == targetName
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return e.Object.GetName() == targetName
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return e.Object.GetName() == targetName
		},
	}
}

func (r *ManagedNodeReconciler) updateNodeLabels(ctx context.Context, node *commonv1.ManagedNode) error {
	labels := labels.DefaultLabels(ctx, r.system.Node())

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
		r.logger.Info("updating labels", "labels", nodeLabels)

		f := func() error {
			return r.Update(ctx, node)
		}

		if err := retry.RetryOnConflict(retry.DefaultBackoff, f); err != nil {
			return fmt.Errorf("failed to update ManagedNode: %w", err)
		}
	}

	return nil
}

func (r *ManagedNodeReconciler) updateNodeStatus(ctx context.Context, node *commonv1.ManagedNode) error {
	info := r.system.Node().Info(ctx)
	node.Status.Release = info.OS.Release
	node.Status.Interfaces = collectNetworkInterfaces()
	node.Status.SSHHostKeys = collectSSHHostKeys(ctx, r.system.Exec(), node.Name)

	liveWG := collectWireGuardInterfaces(ctx, r.system.Exec())

	if node.Spec.WireGuard.Enabled {
		iface := node.Spec.WireGuard.Interface
		if iface == "" {
			iface = "wg0"
		}
		pubKey, err := r.ensureWireGuardKeySecret(ctx, node.Namespace, node.Name, iface)
		if err != nil {
			r.logger.Warn("failed to ensure WireGuard key secret", "err", err)
		} else {
			liveWG = mergeBootstrappedWireGuardKey(liveWG, iface, pubKey)
		}
	}

	node.Status.WireGuard = liveWG

	r.logger.Info("updating node status", "node", node.Name, "release", node.Status.Release)

	f := func() error {
		return r.Status().Update(ctx, node)
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, f); err != nil {
		return fmt.Errorf("failed to update ManagedNode status: %w", err)
	}

	return nil
}

// ensureWireGuardKeySecret returns the public key for the given WireGuard
// interface on this node.  If the backing Secret does not yet exist it is
// created with a freshly generated Curve25519 keypair.  Returns ("", nil)
// only when key generation itself fails (caller logs and continues).
func (r *ManagedNodeReconciler) ensureWireGuardKeySecret(ctx context.Context, namespace, nodeName, iface string) (string, error) {
	secretName := "wg-" + iface + "-" + nodeName

	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, &secret)
	if err == nil {
		return string(secret.Data["public-key"]), nil
	}
	if !k8serrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to get WireGuard key secret %s: %w", secretName, err)
	}

	// Secret doesn't exist — generate a new keypair.
	privKey, pubKey, err := generateWireGuardKeyPair()
	if err != nil {
		return "", fmt.Errorf("failed to generate WireGuard keypair: %w", err)
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"private-key": []byte(privKey),
			"public-key":  []byte(pubKey),
		},
	}

	r.logger.Info("creating WireGuard key secret", "secret", secretName)
	if err := r.Create(ctx, newSecret); err != nil {
		return "", fmt.Errorf("failed to create WireGuard key secret %s: %w", secretName, err)
	}

	return pubKey, nil
}

// collectNetworkInterfaces enumerates non-loopback, up interfaces and returns
// their unicast addresses grouped by interface name.
func collectNetworkInterfaces() map[string]commonv1.NetworkInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	result := make(map[string]commonv1.NetworkInterface)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		var ni commonv1.NetworkInterface
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if ip.To4() != nil {
				ni.IPv4 = append(ni.IPv4, ip.String())
			} else if ip.IsGlobalUnicast() {
				ni.IPv6 = append(ni.IPv6, ip.String())
			}
		}

		if len(ni.IPv4) > 0 || len(ni.IPv6) > 0 {
			result[iface.Name] = ni
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// collectSSHHostKeys runs ssh-keygen -r <hostname> and parses the SSHFP
// record lines it emits. Each line has the form:
//
//	<hostname> IN SSHFP <algorithm> <fp-type> <fingerprint-hex>
//
// Returns nil if ssh-keygen is unavailable or produces no output.
func collectSSHHostKeys(ctx context.Context, exec handler.ExecHandler, hostname string) []commonv1.SSHHostKey {
	out, _, err := exec.RunCommand(ctx, "ssh-keygen", "-r", hostname)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}

	var keys []commonv1.SSHHostKey
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// Expected: <name> IN SSHFP <alg> <fp-type> <fingerprint>
		if len(fields) != 6 || fields[2] != "SSHFP" {
			continue
		}
		alg, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		fpType, err := strconv.Atoi(fields[4])
		if err != nil {
			continue
		}
		keys = append(keys, commonv1.SSHHostKey{
			Algorithm:       alg,
			FingerprintType: fpType,
			Fingerprint:     fields[5],
		})
	}
	return keys
}

func (r *ManagedNodeReconciler) handleUpgrade(ctx context.Context, node *commonv1.ManagedNode) (time.Time, error) {
	var (
		err        error
		next, last time.Time
		delay      time.Duration
	)

	// NOTE: we require that the node spec has an upgrade schedule, and delay.
	// If the group is present, then we use the locker using the group to ensure
	// that only one member of the group is upgrading at a time.

	// Check if we are a node that performs upgrades
	if node.Spec.Upgrade.Schedule == "" {
		r.logger.Info("managed node has no upgrade schedule")
		return time.Time{}, nil
	}

	if node.Spec.Upgrade.Delay == "" {
		r.logger.Info("managed node has no upgrade delay")
		return time.Time{}, nil
	}

	delay, err = time.ParseDuration(node.Spec.Upgrade.Delay)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse upgrade delay: %w", err)
	}

	next, err = r.nextUpgradeTime(node.Spec.Upgrade.Schedule)
	if err != nil {
		return time.Time{}, err
	}

	last, err = r.lastUpgradeTime(node)
	if err != nil {
		return time.Time{}, err
	}

	var req types.NamespacedName

	// Only lock if we have an upgrade group set
	if node.Spec.Upgrade.Group != "" {
		req = types.NamespacedName{
			Name:      node.Spec.Upgrade.Group,
			Namespace: node.Namespace,
		}

		// If we have the lock, assume we have just upgraded.
		if r.locker.Locked(ctx, req) {
			if err := r.locker.Unlock(ctx, req); err != nil {
				r.logger.Warn("failed to release upgrade lock", "lease", req, "err", err)
			}
			return next, nil
		}
	}

	// Skip an upgrade if an upgrade has already happened within the delay window.
	if !last.IsZero() && time.Since(last) < delay {
		return next, nil
	}

	r.logger.Info("next upgrade time", "schedule", node.Spec.Upgrade.Schedule, "until", time.Until(next))

	// Check if next is within the forgiveness period
	if time.Since(next) < r.cfg.ForgivenessPeriod {
		// If the next upgrade time is less than a minute in the past, we execute immediately.
	} else if time.Until(next) < r.cfg.ForgivenessPeriod {
		// If the upgrade time is less than a minute in the future, requeue and let controller-runtime wake us.
		return next, nil
	} else {
		// If we are outside of the minute range in either direction, return the time which we should check again.
		return next, nil
	}

	// Proceed with the upgrade

	if node.Spec.Upgrade.Group != "" {
		err = r.locker.Lock(ctx, req)
		if err != nil {
			return time.Time{}, err
		}
	}

	upgradeStart := time.Now()

	// Cordon and drain if this host is a Kubernetes node
	k8sNode, err := r.getKubernetesNode(ctx, node.Name)
	if err != nil {
		upgradeDuration.WithLabelValues(node.Name).Observe(time.Since(upgradeStart).Seconds())
		upgradeTotal.WithLabelValues(node.Name, "error").Inc()
		return time.Time{}, err
	}
	if k8sNode != nil {
		if err = r.cordonNode(ctx, node, k8sNode); err != nil {
			upgradeDuration.WithLabelValues(node.Name).Observe(time.Since(upgradeStart).Seconds())
			upgradeTotal.WithLabelValues(node.Name, "error").Inc()
			return time.Time{}, err
		}
		if err = r.drainNode(ctx, node.Name); err != nil {
			r.logger.Warn("drain did not complete cleanly, proceeding with upgrade", "err", err)
		}
	}

	err = r.system.Package().UpgradeAll(ctx)
	if err != nil {
		upgradeDuration.WithLabelValues(node.Name).Observe(time.Since(upgradeStart).Seconds())
		upgradeTotal.WithLabelValues(node.Name, "error").Inc()
		packageOperationsTotal.WithLabelValues(node.Name, "upgrade", "error").Inc()
		return next.Add(delay), err
	}
	packageOperationsTotal.WithLabelValues(node.Name, "upgrade", "success").Inc()

	err = r.system.Node().Upgrade(ctx)
	if err != nil {
		upgradeDuration.WithLabelValues(node.Name).Observe(time.Since(upgradeStart).Seconds())
		upgradeTotal.WithLabelValues(node.Name, "error").Inc()
		return next.Add(delay), err
	}

	upgradeDuration.WithLabelValues(node.Name).Observe(time.Since(upgradeStart).Seconds())
	upgradeTotal.WithLabelValues(node.Name, "success").Inc()

	// Set the upgrade time

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}

	now := time.Now()
	node.Annotations[common.AnnotationLastUpgrade] = now.Format(time.RFC3339)
	lastUpgradeTimestamp.WithLabelValues(node.Name).Set(float64(now.Unix()))

	f := func() error {
		return r.Update(ctx, node)
	}

	if err = retry.RetryOnConflict(retry.DefaultBackoff, f); err != nil {
		return time.Time{}, fmt.Errorf("failed to set last upgrade time: %w", err)
	}

	// Reboot the system after we've marked the system as upgraded
	r.system.Node().Reboot(ctx)

	return time.Time{}, err
}

func (r *ManagedNodeReconciler) lastUpgradeTime(node *commonv1.ManagedNode) (time.Time, error) {
	if last, ok := node.Annotations[common.AnnotationLastUpgrade]; ok {
		r.logger.Info("last upgrade time found", "lastUpgrade", last)

		lastUpgrade, err := time.Parse(time.RFC3339, last)
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to parse LastUpgrade annotation: %w", err)
		}

		return lastUpgrade, nil
	}

	return time.Time{}, nil
}

func (r *ManagedNodeReconciler) nextUpgradeTime(schedule string) (time.Time, error) {
	cron, err := cronexpr.Parse(schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse schedule as cron expression: %w", err)
	}

	return cron.Next(time.Now().Add(-r.cfg.ForgivenessPeriod)), nil
}

// getKubernetesNode returns the k8s Node with the given hostname, or nil if not found.
func (r *ManagedNodeReconciler) getKubernetesNode(ctx context.Context, hostname string) (*corev1.Node, error) {
	node, err := r.clientset.CoreV1().Nodes().Get(ctx, hostname, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.logger.Info("not a kubernetes node, skipping drain", "hostname", hostname)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get kubernetes node: %w", err)
	}
	return node, nil
}

// cordonNode marks the Kubernetes node unschedulable and records the cordon on the ManagedNode.
func (r *ManagedNodeReconciler) cordonNode(ctx context.Context, managedNode *commonv1.ManagedNode, k8sNode *corev1.Node) error {
	patch := []byte(`{"spec":{"unschedulable":true}}`)
	if _, err := r.clientset.CoreV1().Nodes().Patch(ctx, k8sNode.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("failed to cordon kubernetes node: %w", err)
	}
	r.logger.Info("cordoned kubernetes node", "node", k8sNode.Name)

	if managedNode.Annotations == nil {
		managedNode.Annotations = make(map[string]string)
	}
	managedNode.Annotations[common.AnnotationKubernetesNodeCordoned] = time.Now().Format(time.RFC3339)

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		return r.Update(ctx, managedNode)
	}); err != nil {
		return fmt.Errorf("failed to set cordon annotation on ManagedNode: %w", err)
	}
	return nil
}

// drainNode evicts all non-DaemonSet, non-mirror pods and waits for them to terminate.
func (r *ManagedNodeReconciler) drainNode(ctx context.Context, hostname string) error {
	podList, err := r.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", hostname),
	})
	if err != nil {
		return fmt.Errorf("failed to list pods on node: %w", err)
	}

	for _, pod := range podList.Items {
		if !isDrainablePod(pod) {
			continue
		}
		eviction := &policyv1beta1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		if err := r.clientset.CoreV1().Pods(pod.Namespace).Evict(ctx, eviction); err != nil {
			switch {
			case k8serrors.IsNotFound(err):
				// already gone
			case k8serrors.IsTooManyRequests(err):
				r.logger.Info("eviction blocked by PodDisruptionBudget, will retry",
					"pod", pod.Name, "namespace", pod.Namespace)
			default:
				r.logger.Warn("failed to evict pod", "pod", pod.Name,
					"namespace", pod.Namespace, "err", err)
			}
		}
	}

	deadline := time.Now().Add(r.cfg.DrainTimeout)
	for {
		current, err := r.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("spec.nodeName=%s", hostname),
		})
		if err != nil {
			return fmt.Errorf("failed to list pods during drain: %w", err)
		}

		var remaining int
		for _, pod := range current.Items {
			if isDrainablePod(pod) {
				remaining++
			}
		}
		if remaining == 0 {
			r.logger.Info("all pods drained from node", "hostname", hostname)
			return nil
		}

		if time.Now().After(deadline) {
			break
		}

		r.logger.Info("waiting for pods to drain", "hostname", hostname, "remaining", remaining)
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("drain timed out after %s", r.cfg.DrainTimeout)
}

// uncordonNode marks the Kubernetes node schedulable and removes the cordon annotation from the ManagedNode.
func (r *ManagedNodeReconciler) uncordonNode(ctx context.Context, managedNode *commonv1.ManagedNode) error {
	k8sNode, err := r.getKubernetesNode(ctx, managedNode.Name)
	if err != nil {
		return err
	}

	if k8sNode != nil {
		patch := []byte(`{"spec":{"unschedulable":false}}`)
		if _, err := r.clientset.CoreV1().Nodes().Patch(ctx, k8sNode.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to uncordon kubernetes node: %w", err)
		}
		r.logger.Info("uncordoned kubernetes node", "node", k8sNode.Name)
	}

	delete(managedNode.Annotations, common.AnnotationKubernetesNodeCordoned)

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		return r.Update(ctx, managedNode)
	}); err != nil {
		return fmt.Errorf("failed to remove cordon annotation from ManagedNode: %w", err)
	}
	return nil
}

// maybeUncordon uncordons the Kubernetes node if this controller previously cordoned it.
func (r *ManagedNodeReconciler) maybeUncordon(ctx context.Context, managedNode *commonv1.ManagedNode) error {
	if managedNode.Annotations == nil {
		return nil
	}
	if _, ok := managedNode.Annotations[common.AnnotationKubernetesNodeCordoned]; !ok {
		return nil
	}
	return r.uncordonNode(ctx, managedNode)
}

// isDrainablePod returns true if the pod should be evicted during a node drain.
func isDrainablePod(pod corev1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		return false
	}
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return false
	}
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return false
		}
	}
	if _, ok := pod.Annotations["kubernetes.io/config.mirror"]; ok {
		return false
	}
	return true
}
