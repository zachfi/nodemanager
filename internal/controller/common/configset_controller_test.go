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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/locker"
)

var _ = Describe("ConfigSet Controller", func() {
	Context("When two ConfigSets claim the same file", func() {
		const csA = "conflict-a"
		const csB = "conflict-b"
		ctx := context.Background()

		BeforeEach(func() {
			By("creating two ConfigSets with overlapping file paths")
			for _, name := range []string{csA, csB} {
				cs := &commonv1.ConfigSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs)
				if err != nil && errors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
						ObjectMeta: metav1.ObjectMeta{
							Name:      name,
							Namespace: "default",
						},
						Spec: commonv1.ConfigSetSpec{
							Files: []commonv1.File{
								{Path: "/etc/conflict-test.conf", Ensure: "file", Content: name},
							},
						},
					})).To(Succeed())
				}
			}
		})

		AfterEach(func() {
			for _, name := range []string{csA, csB} {
				cs := &commonv1.ConfigSet{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs); err == nil {
					Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
				}
			}
		})

		It("should block both ConfigSets and record conflicts in status", func() {
			lkr := locker.NewLeaseLocker(ctx, logger, locker.Config{}, clientset, "default", hostname)

			for _, name := range []string{csA, csB} {
				sys := &mockSystemHandler{}
				reconciler := &ConfigSetReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					tracer: noop.NewTracerProvider().Tracer("test"),
					logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
					system: sys,
					locker: lkr,
				}
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
				})
				Expect(err).NotTo(HaveOccurred())

				By("checking no file writes were attempted for " + name)
				Expect(sys.File().(*mockFileHandler).fileWriteCalls).To(BeEmpty())
			}

			By("checking ManagedNode status records conflicts for both ConfigSets")
			mn := &commonv1.ManagedNode{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hostname, Namespace: "default"}, mn)).To(Succeed())
			for _, name := range []string{csA, csB} {
				var found *commonv1.ConfigSetApplyStatus
				for i := range mn.Status.ConfigSets {
					if mn.Status.ConfigSets[i].Name == name {
						found = &mn.Status.ConfigSets[i]
						break
					}
				}
				Expect(found).NotTo(BeNil(), "expected configset %s in ManagedNode status", name)
				Expect(found.Conflicts).NotTo(BeEmpty(), "expected conflicts for %s", name)
			}

			By("checking ConfigSet conditions reflect the conflict")
			for _, name := range []string{csA, csB} {
				cs := &commonv1.ConfigSet{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs)).To(Succeed())
				var conflicted *metav1.Condition
				for i := range cs.Status.Conditions {
					if cs.Status.Conditions[i].Type == "Conflicted" {
						conflicted = &cs.Status.Conditions[i]
						break
					}
				}
				Expect(conflicted).NotTo(BeNil(), "expected Conflicted condition on configset %s", name)
				Expect(conflicted.Status).To(Equal(metav1.ConditionTrue))
			}
		})
	})

	Context("When ConfigSets have overlapping files but different label selectors", func() {
		const csMatch = "selector-match"
		const csNoMatch = "selector-nomatch"
		ctx := context.Background()

		BeforeEach(func() {
			By("creating a matching ConfigSet and a non-matching ConfigSet with the same file path")
			cs := &commonv1.ConfigSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: csMatch, Namespace: "default"}, cs)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      csMatch,
						Namespace: "default",
					},
					Spec: commonv1.ConfigSetSpec{
						Files: []commonv1.File{
							{Path: "/etc/selector-test.conf", Ensure: "absent"},
						},
					},
				})).To(Succeed())
			}
			cs2 := &commonv1.ConfigSet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: csNoMatch, Namespace: "default"}, cs2)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
					ObjectMeta: metav1.ObjectMeta{
						// Label that won't match our test node
						Name:      csNoMatch,
						Namespace: "default",
						Labels:    map[string]string{"kubernetes.io/hostname": "nonexistent-node"},
					},
					Spec: commonv1.ConfigSetSpec{
						Files: []commonv1.File{
							{Path: "/etc/selector-test.conf", Ensure: "absent"},
						},
					},
				})).To(Succeed())
			}
		})

		AfterEach(func() {
			for _, name := range []string{csMatch, csNoMatch} {
				cs := &commonv1.ConfigSet{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs); err == nil {
					Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
				}
			}
		})

		It("should not conflict because the non-matching ConfigSet does not apply to this node", func() {
			sys := &mockSystemHandler{}
			lkr := locker.NewLeaseLocker(ctx, logger, locker.Config{}, clientset, "default", hostname)
			reconciler := &ConfigSetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
				locker: lkr,
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: csMatch, Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("checking ManagedNode status has no conflicts for the matching ConfigSet")
			mn := &commonv1.ManagedNode{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hostname, Namespace: "default"}, mn)).To(Succeed())
			for _, entry := range mn.Status.ConfigSets {
				if entry.Name == csMatch {
					Expect(entry.Conflicts).To(BeEmpty())
					return
				}
			}
		})
	})

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
								LockGroup: "testing",
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

			locker := locker.NewLeaseLocker(ctx, logger, locker.Config{}, clientset, typeNamespacedName.Namespace, hostname)

			controllerReconciler := &ConfigSetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: systemHandler,
				locker: locker,
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
			Expect(systemHandler.Service().(*mockServiceHandler).restartCalls).ToNot(HaveKey("chronyd"))
		})
	})
})
