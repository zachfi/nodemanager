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
	"os"
	"time"

	"github.com/grafana/dskit/backoff"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace/noop"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/common"
	"github.com/zachfi/nodemanager/pkg/locker"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

var lockerConfig = locker.Config{
	Backoff: backoff.Config{
		MinBackoff: 100 * time.Millisecond,
		MaxBackoff: 200 * time.Millisecond,
	},
	LeaseDuration: 20 * time.Second,
}

var _ = Describe("isDrainablePod", func() {
	DescribeTable("pod filtering",
		func(pod corev1.Pod, expected bool) {
			Expect(isDrainablePod(pod)).To(Equal(expected))
		},
		Entry("regular pod is drainable",
			corev1.Pod{},
			true,
		),
		Entry("DaemonSet-owned pod is not drainable",
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "DaemonSet", Name: "my-ds"},
					},
				},
			},
			false,
		),
		Entry("mirror pod is not drainable",
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"kubernetes.io/config.mirror": "abc123"},
				},
			},
			false,
		),
		Entry("ReplicaSet-owned pod is drainable",
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "ReplicaSet", Name: "my-rs"},
					},
				},
			},
			true,
		),
		Entry("already-terminating pod is not drainable",
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
			},
			false,
		),
		Entry("completed pod (Succeeded) is not drainable",
			corev1.Pod{
				Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
			},
			false,
		),
		Entry("failed pod is not drainable",
			corev1.Pod{
				Status: corev1.PodStatus{Phase: corev1.PodFailed},
			},
			false,
		),
	)
})

var _ = Describe("ManagedNode Controller", func() {
	Context("When the managed node is also a Kubernetes node", func() {
		const resourceName = "test-node"
		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

		BeforeEach(func() {
			By("creating the ManagedNode")
			mn := &commonv1.ManagedNode{}
			err := k8sClient.Get(ctx, typeNamespacedName, mn)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &commonv1.ManagedNode{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: commonv1.ManagedNodeSpec{
						Domain: "example.com",
						Upgrade: commonv1.Upgrade{
							Group:    "stable",
							Schedule: "* * * * * * *",
							Delay:    "100ms",
						},
					},
				})).To(Succeed())
			}

			By("creating a matching Kubernetes Node")
			_, err = clientset.CoreV1().Nodes().Create(ctx, &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName},
			}, metav1.CreateOptions{})
			if err != nil && !errors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		AfterEach(func() {
			mn := &commonv1.ManagedNode{}
			if err := k8sClient.Get(ctx, typeNamespacedName, mn); err == nil {
				Expect(k8sClient.Delete(ctx, mn)).To(Succeed())
			}
			_ = clientset.CoreV1().Nodes().Delete(ctx, resourceName, metav1.DeleteOptions{})
		})

		It("should cordon the Kubernetes node before upgrading", func() {
			sys := &mockSystemHandler{}
			controllerReconciler := &ManagedNodeReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				tracer:    noop.NewTracerProvider().Tracer("test"),
				logger:    logger,
				system:    sys,
				locker:    locker.NewLeaseLocker(ctx, logger, lockerConfig, clientset, "default", resourceName),
				clientset: clientset,
				cfg:       ManagedNodeConfig{DrainTimeout: 100 * time.Millisecond},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("checking ManagedNode has the cordon status")
			mn := &commonv1.ManagedNode{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, mn)).To(Succeed())
			Expect(mn.Status.KubernetesNodeCordoned).NotTo(BeNil())

			By("checking the Kubernetes node is unschedulable")
			k8sNode, err := clientset.CoreV1().Nodes().Get(ctx, resourceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sNode.Spec.Unschedulable).To(BeTrue())

			By("checking upgrade and reboot were called")
			Expect(sys.Node().(*mockNodeHandler).upgradeCalls).To(Equal(1))
			Expect(sys.Node().(*mockNodeHandler).rebootCalls).To(Equal(1))
		})
	})

	Context("When the managed node was cordoned before reboot", func() {
		const resourceName = "test-node"
		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

		BeforeEach(func() {
			By("creating the ManagedNode with cordon status and recent upgrade")
			mn := &commonv1.ManagedNode{}
			err := k8sClient.Get(ctx, typeNamespacedName, mn)
			if err != nil && errors.IsNotFound(err) {
				mn = &commonv1.ManagedNode{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: commonv1.ManagedNodeSpec{
						Domain: "example.com",
						Upgrade: commonv1.Upgrade{
							Group:    "stable",
							Schedule: "* * * * * * *",
							Delay:    "1h",
						},
					},
				}
				Expect(k8sClient.Create(ctx, mn)).To(Succeed())

				now := metav1.Now()
				mn.Status.KubernetesNodeCordoned = &now
				mn.Status.LastUpgrade = &now
				Expect(k8sClient.Status().Update(ctx, mn)).To(Succeed())
			}

			By("creating a cordoned Kubernetes Node")
			_, err = clientset.CoreV1().Nodes().Create(ctx, &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName},
				Spec:       corev1.NodeSpec{Unschedulable: true},
			}, metav1.CreateOptions{})
			if err != nil && !errors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		AfterEach(func() {
			mn := &commonv1.ManagedNode{}
			if err := k8sClient.Get(ctx, typeNamespacedName, mn); err == nil {
				Expect(k8sClient.Delete(ctx, mn)).To(Succeed())
			}
			_ = clientset.CoreV1().Nodes().Delete(ctx, resourceName, metav1.DeleteOptions{})
		})

		It("should uncordon the Kubernetes node and remove the annotation", func() {
			sys := &mockSystemHandler{}
			controllerReconciler := &ManagedNodeReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				tracer:    noop.NewTracerProvider().Tracer("test"),
				logger:    logger,
				system:    sys,
				locker:    locker.NewLeaseLocker(ctx, logger, lockerConfig, clientset, "default", resourceName),
				clientset: clientset,
				cfg:       ManagedNodeConfig{DrainTimeout: 100 * time.Millisecond},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("checking ManagedNode no longer has the cordon status")
			mn := &commonv1.ManagedNode{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, mn)).To(Succeed())
			Expect(mn.Status.KubernetesNodeCordoned).To(BeNil())

			By("checking the Kubernetes node is schedulable again")
			k8sNode, err := clientset.CoreV1().Nodes().Get(ctx, resourceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sNode.Spec.Unschedulable).To(BeFalse())

			By("checking upgrade and reboot were not called")
			Expect(sys.Node().(*mockNodeHandler).upgradeCalls).To(Equal(0))
			Expect(sys.Node().(*mockNodeHandler).rebootCalls).To(Equal(0))
		})
	})

	Context("When reconciling a resource", func() {
		const resourceName = "test-node"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		managednode := &commonv1.ManagedNode{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ManagedNode")
			err := k8sClient.Get(ctx, typeNamespacedName, managednode)
			if err != nil && errors.IsNotFound(err) {
				resource := &commonv1.ManagedNode{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
						Labels:    map[string]string{},
					},
					Spec: commonv1.ManagedNodeSpec{
						Domain: "example.com",
						Upgrade: commonv1.Upgrade{
							Group:    "stable",
							Schedule: "* * * * * * *",
							Delay:    "100ms",
						},
					},
					Status: commonv1.ManagedNodeStatus{
						Release: "v1.0.0",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &commonv1.ManagedNode{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ManagedNode")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile a basic resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ManagedNodeReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				tracer:    noop.NewTracerProvider().Tracer("test"),
				logger:    logger,
				system:    systemHandler,
				locker:    locker.NewLeaseLocker(ctx, logger, lockerConfig, clientset, typeNamespacedName.Namespace, hostname),
				clientset: clientset,
				cfg:       ManagedNodeConfig{DrainTimeout: 100 * time.Millisecond},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Upgrade and reboot handling
			Expect(systemHandler.Node().(*mockNodeHandler).upgradeCalls).To(Equal(1), "Expected the NodeHandler to have been called for upgrade.")
			Expect(systemHandler.Node().(*mockNodeHandler).rebootCalls).To(Equal(1), "Expected the NodeHandler to have been called for reboot.")

			k8sClient.Get(ctx, typeNamespacedName, managednode)

			// The node should have the last upgrade status set
			Expect(managednode.Status.LastUpgrade).NotTo(BeNil(), "Expected the last upgrade status to be set after reconciliation.")

			leaseName := types.NamespacedName{
				Name:      managednode.Spec.Upgrade.Group,
				Namespace: "default",
			}
			lease := &coordinationv1.Lease{}

			err = k8sClient.Get(ctx, leaseName, lease)
			Expect(err).ToNot(HaveOccurred())

			Expect(*lease.Spec.HolderIdentity).To(Equal(hostname))
			Expect(controllerReconciler.locker.Locked(ctx, leaseName)).To(Equal(true))

			logger.Info("leaseName", "leaseName", fmt.Sprintf("%+v", leaseName))
			logger.Info("lease", "lease", fmt.Sprintf("%+v", lease))

			// A second reconcile should remove the lock, but not call upgrade or reboot .
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// We did not increment the counters
			Expect(systemHandler.Node().(*mockNodeHandler).upgradeCalls).To(Equal(1), "Expected the NodeHandler to have been called for upgrade.")
			Expect(systemHandler.Node().(*mockNodeHandler).rebootCalls).To(Equal(1), "Expected the NodeHandler to have been called for reboot.")

			k8sClient.Get(ctx, typeNamespacedName, managednode)

			// Check that we no longer hold the lease
			err = k8sClient.Get(ctx, leaseName, lease)
			Expect(err).ToNot(HaveOccurred())
			Expect(lease.Spec.HolderIdentity).To(BeNil())
			Expect(controllerReconciler.locker.Locked(ctx, leaseName)).To(Equal(false))

			Expect(managednode.Status.LastUpgrade).NotTo(BeNil(), "Expected LastUpgrade to be set after reconciliation.")
			Expect(managednode.Status.LastUpgrade.Time).NotTo(BeZero(), "Expected LastUpgrade time to be non-zero.")
			Expect(managednode.Status.LastUpgrade.Time).To(BeTemporally("~", metav1.Now().Time, 5*time.Second), "Expected LastUpgrade to be close to the current time after reconciliation.")
		})
	})

	Context("When the managed node has an upgrade hold annotation", func() {
		const resourceName = "test-hold-node"
		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

		BeforeEach(func() {
			By("creating the ManagedNode with the upgrade hold annotation")
			mn := &commonv1.ManagedNode{}
			err := k8sClient.Get(ctx, typeNamespacedName, mn)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &commonv1.ManagedNode{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
						Annotations: map[string]string{
							common.AnnotationUpgradeHold: "true",
						},
					},
					Spec: commonv1.ManagedNodeSpec{
						Domain: "example.com",
						Upgrade: commonv1.Upgrade{
							Schedule: "* * * * * * *",
							Delay:    "0s",
						},
					},
				})).To(Succeed())
			}
		})

		AfterEach(func() {
			mn := &commonv1.ManagedNode{}
			if err := k8sClient.Get(ctx, typeNamespacedName, mn); err == nil {
				Expect(k8sClient.Delete(ctx, mn)).To(Succeed())
			}
		})

		It("should not call upgrade even when the schedule is due", func() {
			sys := &mockSystemHandler{}
			controllerReconciler := &ManagedNodeReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				tracer:    noop.NewTracerProvider().Tracer("test"),
				logger:    logger,
				system:    sys,
				locker:    locker.NewLeaseLocker(ctx, logger, lockerConfig, clientset, "default", resourceName),
				clientset: clientset,
				cfg:       ManagedNodeConfig{DrainTimeout: 100 * time.Millisecond},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("checking upgrade was not called")
			Expect(sys.Node().(*mockNodeHandler).upgradeCalls).To(Equal(0))

			By("checking the hold annotation is still present")
			mn := &commonv1.ManagedNode{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, mn)).To(Succeed())
			Expect(mn.Annotations).To(HaveKey(common.AnnotationUpgradeHold))
		})
	})
})
