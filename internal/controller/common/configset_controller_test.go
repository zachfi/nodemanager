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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/locker"
	"github.com/zachfi/nodemanager/pkg/services"
)

var _ = Describe("ConfigSet Controller", func() {
	Context("When two ConfigSets claim the same file", func() {
		const csA = "conflict-a"
		const csB = "conflict-b"
		ctx := context.Background()

		BeforeEach(func() {
			ensureLocalNodeLabel(ctx, "nodemanager.test/enabled", "true")

			By("creating two ConfigSets with overlapping file paths")
			for _, name := range []string{csA, csB} {
				cs := &commonv1.ConfigSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs)
				if err != nil && errors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
						ObjectMeta: metav1.ObjectMeta{
							Name:      name,
							Namespace: "default",
							Labels:    map[string]string{"nodemanager.test/enabled": "true"},
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
			osHostname, _ := os.Hostname()
			mn := &commonv1.ManagedNode{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
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
			ensureLocalNodeLabel(ctx, "nodemanager.test/enabled", "true")

			By("creating a matching ConfigSet and a non-matching ConfigSet with the same file path")
			cs := &commonv1.ConfigSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: csMatch, Namespace: "default"}, cs)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      csMatch,
						Namespace: "default",
						Labels:    map[string]string{"nodemanager.test/enabled": "true"},
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
			osHostname, _ := os.Hostname()
			mn := &commonv1.ManagedNode{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
			for _, entry := range mn.Status.ConfigSets {
				if entry.Name == csMatch {
					Expect(entry.Conflicts).To(BeEmpty())
					return
				}
			}
		})
	})

	Context("When updateConfigSetCondition is called repeatedly with the same state", func() {
		const csIdempotent = "condition-idempotent"
		ctx := context.Background()

		BeforeEach(func() {
			cs := &commonv1.ConfigSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: csIdempotent, Namespace: "default"}, cs)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      csIdempotent,
						Namespace: "default",
					},
				})).To(Succeed())
			}
		})

		AfterEach(func() {
			cs := &commonv1.ConfigSet{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: csIdempotent, Namespace: "default"}, cs); err == nil {
				Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			}
		})

		It("should not update LastTransitionTime when the condition is unchanged", func() {
			r := &ConfigSetReconciler{
				Client:              k8sClient,
				Scheme:              k8sClient.Scheme(),
				tracer:              noop.NewTracerProvider().Tracer("test"),
				logger:              slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				lastResourceVersion: make(map[string]string),
			}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: csIdempotent, Namespace: "default"}}
			conflicts := []string{"file:/etc/foo.conf (also in configset \"other\")"}

			// First call — creates the Conflicted condition.
			Expect(r.updateConfigSetCondition(ctx, req, conflicts)).To(Succeed())

			cs := &commonv1.ConfigSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csIdempotent, Namespace: "default"}, cs)).To(Succeed())
			first := meta.FindStatusCondition(cs.Status.Conditions, "Conflicted")
			Expect(first).NotTo(BeNil())
			Expect(first.Status).To(Equal(metav1.ConditionTrue))
			firstTransition := first.LastTransitionTime

			// Second call — same conflicts, must be a no-op (no API write).
			Expect(r.updateConfigSetCondition(ctx, req, conflicts)).To(Succeed())

			cs2 := &commonv1.ConfigSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csIdempotent, Namespace: "default"}, cs2)).To(Succeed())
			second := meta.FindStatusCondition(cs2.Status.Conditions, "Conflicted")
			Expect(second).NotTo(BeNil())
			Expect(second.LastTransitionTime).To(Equal(firstTransition), "LastTransitionTime must not change when conflict is unchanged")
		})
	})

	Context("When the local ManagedNode changes", func() {
		const csWatch1 = "watch-cs-one"
		const csWatch2 = "watch-cs-two"
		ctx := context.Background()

		BeforeEach(func() {
			for _, name := range []string{csWatch1, csWatch2} {
				cs := &commonv1.ConfigSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs)
				if err != nil && errors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
						ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
					})).To(Succeed())
				}
			}
		})

		AfterEach(func() {
			for _, name := range []string{csWatch1, csWatch2} {
				cs := &commonv1.ConfigSet{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs); err == nil {
					Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
				}
			}
		})

		It("configSetsOnNodeChange returns requests only for the matching hostname", func() {
			r := &ConfigSetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				cfg:    ConfigSetConfig{Namespace: "default"},
			}
			mapper := r.configSetsOnNodeChange(hostname)

			By("returning nil for a ManagedNode with a different name")
			other := &commonv1.ManagedNode{ObjectMeta: metav1.ObjectMeta{Name: "other-node", Namespace: "default"}}
			Expect(mapper(ctx, other)).To(BeNil())

			By("returning one request per ConfigSet for the matching hostname")
			local := &commonv1.ManagedNode{ObjectMeta: metav1.ObjectMeta{Name: hostname, Namespace: "default"}}
			reqs := mapper(ctx, local)
			Expect(reqs).NotTo(BeEmpty())
			names := make([]string, len(reqs))
			for i, r := range reqs {
				names[i] = r.Name
			}
			Expect(names).To(ContainElements(csWatch1, csWatch2))
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
			ensureLocalNodeLabel(ctx, "nodemanager.test/enabled", "true")

			By("creating the custom resource for the Kind ConfigSet")
			err := k8sClient.Get(ctx, typeNamespacedName, configset)
			if err != nil && errors.IsNotFound(err) {
				resource := &commonv1.ConfigSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
						Labels:    map[string]string{"nodemanager.test/enabled": "true"},
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

	Context("When a service has a managed rc.conf.d file", func() {
		ctx := context.Background()

		It("should skip Enable/Disable/SetArguments when the rc.conf.d file is managed", func() {
			sys := &mockSystemHandler{}
			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
			}

			services := []commonv1.Service{
				{Name: "unbound_exporter", Enable: true, Ensure: "running", Arguments: "some-args"},
			}
			files := []commonv1.File{
				{Path: "/etc/rc.conf.d/unbound_exporter", Ensure: "file", Content: "unbound_exporter_host=localhost"},
			}

			err := r.handleServiceSet(ctx, "test-node", "default", services, files, nil)
			Expect(err).NotTo(HaveOccurred())

			svcMock := sys.Service().(*mockServiceHandler)
			Expect(svcMock.enableCalls).NotTo(HaveKey("unbound_exporter"), "Enable must not be called when rc.conf.d file is managed")
			Expect(svcMock.disableCalls).NotTo(HaveKey("unbound_exporter"), "Disable must not be called when rc.conf.d file is managed")
			Expect(svcMock.setArgsCalls).NotTo(HaveKey("unbound_exporter"), "SetArguments must not be called when rc.conf.d file is managed")
		})

		It("should call Enable/Disable/SetArguments when the rc.conf.d file is not managed", func() {
			sys := &mockSystemHandler{}
			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
			}

			services := []commonv1.Service{
				{Name: "unbound_exporter", Enable: true, Ensure: "running", Arguments: "some-args"},
			}

			err := r.handleServiceSet(ctx, "test-node", "default", services, nil, nil)
			Expect(err).NotTo(HaveOccurred())

			svcMock := sys.Service().(*mockServiceHandler)
			Expect(svcMock.enableCalls).To(HaveKey("unbound_exporter"), "Enable must be called when rc.conf.d file is not managed")
			Expect(svcMock.setArgsCalls).To(HaveKey("unbound_exporter"), "SetArguments must be called when rc.conf.d file is not managed")
		})

		It("should skip Disable when the rc.conf.d file is managed and enable is false", func() {
			sys := &mockSystemHandler{}
			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
			}

			services := []commonv1.Service{
				{Name: "myservice", Enable: false, Ensure: "stopped"},
			}
			files := []commonv1.File{
				{Path: "/etc/rc.conf.d/myservice", Ensure: "file", Content: "myservice_enable=NO"},
			}

			err := r.handleServiceSet(ctx, "test-node", "default", services, files, nil)
			Expect(err).NotTo(HaveOccurred())

			svcMock := sys.Service().(*mockServiceHandler)
			Expect(svcMock.disableCalls).NotTo(HaveKey("myservice"), "Disable must not be called when rc.conf.d file is managed")
		})
	})

	Context("Service start verification", func() {
		ctx := context.Background()

		It("records result=exited when the daemon is not Running after StartVerifyDelay", func() {
			sys := &mockSystemHandler{}
			// Pre-Start status is Stopped (mock default), so handleServiceSet
			// calls Start; after Start, the daemon is still Stopped, simulating
			// the nslcd parse-error case where rc.d returns 0 but the daemon
			// self-aborted.
			svc := sys.Service().(*mockServiceHandler)
			svc.serviceStatus = map[string]services.ServiceStatus{"badd": services.Stopped}

			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
				cfg:    ConfigSetConfig{StartVerifyDelay: 5 * time.Millisecond},
			}

			svcs := []commonv1.Service{
				{Name: "badd", Enable: true, Ensure: "running"},
			}

			err := r.handleServiceSet(ctx, "test-node", "default", svcs, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.startCalls).To(HaveKeyWithValue("badd", 1), "Start should have been called once")
			Expect(svc.statusCalls["badd"]).To(BeNumerically(">=", 2),
				"Status must be polled at least twice: pre-Start check and post-Start verify")
		})

		It("does not re-poll Status when StartVerifyDelay is zero", func() {
			sys := &mockSystemHandler{}
			svc := sys.Service().(*mockServiceHandler)
			svc.serviceStatus = map[string]services.ServiceStatus{"badd": services.Stopped}

			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
				cfg:    ConfigSetConfig{StartVerifyDelay: 0},
			}

			svcs := []commonv1.Service{
				{Name: "badd", Enable: true, Ensure: "running"},
			}

			err := r.handleServiceSet(ctx, "test-node", "default", svcs, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.startCalls).To(HaveKeyWithValue("badd", 1))
			Expect(svc.statusCalls["badd"]).To(Equal(1),
				"with StartVerifyDelay=0, only the pre-Start status check should happen")
		})

		It("records result=exited on Restart when the daemon is not Running afterward", func() {
			sys := &mockSystemHandler{}
			svc := sys.Service().(*mockServiceHandler)
			// Pre-Restart status is Running (so the inline-Start branch is NOT taken;
			// the restart path is exercised via changedFiles intersecting SubscribeFiles).
			// After Restart returns, the daemon is Stopped — simulating the broken-config case.
			svc.serviceStatus = map[string]services.ServiceStatus{"badd": services.Running}

			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
				cfg:    ConfigSetConfig{StartVerifyDelay: 5 * time.Millisecond},
				locker: &noopLocker{},
			}

			svcs := []commonv1.Service{
				{Name: "badd", Enable: true, Ensure: "running", SusbscribeFiles: []string{"/etc/badd.conf"}},
			}

			// Mock Restart sets status to Stopped via the test hook below.
			svc.onRestart = func(_ context.Context, name string) {
				svc.serviceStatus[name] = services.Stopped
			}

			err := r.handleServiceSet(ctx, "test-node", "default", svcs, nil, []string{"/etc/badd.conf"})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.restartCalls).To(HaveKeyWithValue("badd", 1))
			// Status calls: 1 pre-loop check (line 826) + 1 post-Restart verify = 2.
			Expect(svc.statusCalls["badd"]).To(BeNumerically(">=", 2),
				"Status must be polled after Restart to verify the daemon stayed Running")
		})
	})

	Context("Metric label attribution", func() {
		ctx := context.Background()

		It("threads the package name into nodemanager_package_operations_total", func() {
			sys := &mockSystemHandler{}
			pkgs := sys.Package().(*mockPackageHandler)
			// List() returns this map; "vim" looks present (Ensure=absent → remove) and "git" looks absent (Ensure=installed → install).
			pkgs.packageList = map[string]string{"vim": ""}

			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
			}

			packageSet := []commonv1.Package{
				{Name: "git", Ensure: "installed"},
				{Name: "vim", Ensure: "absent"},
			}

			Expect(r.handlePackageSet(ctx, "test-node-pkg-label", packageSet)).To(Succeed())
			Expect(counterValue(packageOperationsTotal, "test-node-pkg-label", "git", "install", "success")).To(Equal(1.0))
			Expect(counterValue(packageOperationsTotal, "test-node-pkg-label", "vim", "remove", "success")).To(Equal(1.0))
		})

		It("emits one nodemanager_file_changes_total series per changed path", func() {
			sys := &mockSystemHandler{}
			r := &ConfigSetReconciler{
				tracer: noop.NewTracerProvider().Tracer("test"),
				logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
				system: sys,
			}

			fileSet := []commonv1.File{
				{Path: "/tmp/nm-test-a", Ensure: "file", Content: "alpha"},
				{Path: "/tmp/nm-test-b", Ensure: "file", Content: "beta"},
			}

			node := commonv1.ManagedNode{}
			changed, _, err := r.handleFileSet(ctx, "test-node-path-label", "cs-path-label", "default", fileSet, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(ConsistOf("/tmp/nm-test-a", "/tmp/nm-test-b"))
			Expect(counterValue(fileChangesTotal, "test-node-path-label", "cs-path-label", "/tmp/nm-test-a", "success")).To(Equal(1.0))
			Expect(counterValue(fileChangesTotal, "test-node-path-label", "cs-path-label", "/tmp/nm-test-b", "success")).To(Equal(1.0))
		})
	})
})
