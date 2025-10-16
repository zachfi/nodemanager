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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
)

var _ = Describe("ConfigSet Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		configset := &commonv1.ConfigSet{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ConfigSet")
			err := k8sClient.Get(ctx, typeNamespacedName, configset)
			if err != nil && errors.IsNotFound(err) {
				resource := &commonv1.ConfigSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: commonv1.ConfigSetSpec{
						Packages: []commonv1.Package{
							{
								Name:   "chrony",
								Ensure: "installed",
							},
						},
						Services: []commonv1.Service{
							{
								Name:      "chronyd",
								Enable:    true,
								Ensure:    "running",
								Arguments: "--config /etc/chrony/chrony.conf",
							},
						},
						Files: []commonv1.File{
							{
								Path:   "/tmp/does/not/exist",
								Ensure: "absent",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &commonv1.ConfigSet{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ConfigSet")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ConfigSetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: systemHandler,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// Check the system mock details
			Expect(systemHandler.Package().(*mockPackageHandler).installCalls).To(HaveKey("chrony"))
			Expect(systemHandler.Service().(*mockServiceHandler).enableCalls).To(HaveKey("chronyd"))
			Expect(systemHandler.Service().(*mockServiceHandler).startCalls).To(HaveKey("chronyd"))
			Expect(systemHandler.Service().(*mockServiceHandler).setArgsCalls).To(HaveKey("chronyd"))
		})
	})
})

func TestConfigSetController(t *testing.T) {
	cases := []struct {
		name string
	}{}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
		})
	}
}
