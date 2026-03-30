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

package freebsd

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

var _ = Describe("Jail Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		jail := &freebsdv1.Jail{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Jail")
			err := k8sClient.Get(ctx, typeNamespacedName, jail)
			if err != nil && errors.IsNotFound(err) {
				resource := &freebsdv1.Jail{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: freebsdv1.JailSpec{
						// NodeName is set to a value that will never match the
						// reconciler's hostname (which is empty in unit tests
						// constructed without NewJailReconciler). This ensures
						// Reconcile returns before touching the nil manager.
						NodeName: "other-host",
						Release:  "14.2-RELEASE",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &freebsdv1.Jail{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Jail")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should skip reconciliation when NodeName does not match", func() {
			By("Reconciling the created resource")
			// Construct the reconciler directly (hostname defaults to ""), so
			// any Jail with a non-empty NodeName will be filtered out and
			// Reconcile will return immediately without invoking the manager.
			controllerReconciler := &JailReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
