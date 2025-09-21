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
	"log/slog"
	"os"
	"time"

	"github.com/grafana/dskit/backoff"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/common"
	"github.com/zachfi/nodemanager/pkg/common/labels"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

var backoffConfig = backoff.Config{
	MinBackoff: 100 * time.Millisecond,
	MaxBackoff: 200 * time.Millisecond,
}

var _ = Describe("ManagedNode Controller", func() {
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
						Labels: map[string]string{
							labels.LabelUpgradeGroup: "stable",
						},
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
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: logger,
				system: systemHandler,
				locker: NewKeyLocker(logger, LockerConfig{Backoff: backoffConfig}, k8sClient, common.AnnotationUpgradeLock),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Upgrade and reboot handling
			Expect(systemHandler.Node().(*mockNodeHandler).upgradeCalls).To(Equal(1), "Expected the NodeHandler to have been called for upgrade.")
			Expect(systemHandler.Node().(*mockNodeHandler).rebootCalls).To(Equal(1), "Expected the NodeHandler to have been called for reboot.")

			k8sClient.Get(ctx, typeNamespacedName, managednode)

			// The node should have the lock annotation set
			Expect(managednode.Annotations).To(HaveKey(common.AnnotationUpgradeLock), "Expected the upgrade lock annotation to be set after reconciliation.")

			// A second reconcile should remove the lock, but not call upgrade or reboot again.
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// We did not increment the counters
			Expect(systemHandler.Node().(*mockNodeHandler).upgradeCalls).To(Equal(1), "Expected the NodeHandler to have been called for upgrade.")
			Expect(systemHandler.Node().(*mockNodeHandler).rebootCalls).To(Equal(1), "Expected the NodeHandler to have been called for reboot.")

			k8sClient.Get(ctx, typeNamespacedName, managednode)

			Expect(managednode.Annotations).ToNot(HaveKey(common.AnnotationUpgradeLock), "Expect the ugprade lock annotation to be removed after reconciliation.")

			lastUpgrade, err := time.Parse(time.RFC3339, managednode.Annotations[common.AnnotationLastUpgrade])
			Expect(err).NotTo(HaveOccurred())

			Expect(lastUpgrade).ToNot(BeZero(), "Expected LastUpgrade to be set after reconciliation.")
			Expect(lastUpgrade).To(BeTemporally("~", metav1.Now().Time, 5*time.Second), "Expected LastUpgrade to be close to the current time after reconciliation.")
		})
	})
})
