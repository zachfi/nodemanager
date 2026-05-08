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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/cmdrunner"
	"github.com/zachfi/nodemanager/pkg/common/labels"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// recordedExec captures one call to ExecHandler so tests can assert which
// poudriere/portshaker subcommands fired and in what order.
type recordedExec struct {
	Command string
	Args    []string
}

// poudriereMockExec is a minimal ExecHandler that records every call and
// returns staged stdout for *List* operations. The reconciler runs
// `poudriere ports -l` and `poudriere jail -l` first to discover existing
// trees and jails; tests stage empty strings for those so the controller
// believes nothing exists yet and proceeds to create everything.
type poudriereMockExec struct {
	mu    sync.Mutex
	calls []recordedExec
	// outputs is consumed in FIFO order by RunCommand. Empty when exhausted.
	outputs []string
}

func (e *poudriereMockExec) record(command string, args []string) (string, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, recordedExec{
		Command: command,
		Args:    append([]string(nil), args...),
	})
	out := ""
	if len(e.outputs) > 0 {
		out, e.outputs = e.outputs[0], e.outputs[1:]
	}
	return out, 0, nil
}

func (e *poudriereMockExec) RunCommand(_ context.Context, command string, args ...string) (string, int, error) {
	return e.record(command, args)
}

func (e *poudriereMockExec) SimpleRunCommand(ctx context.Context, command string, args ...string) error {
	_, _, err := e.RunCommand(ctx, command, args...)
	return err
}

func (e *poudriereMockExec) RunCommandWithInput(ctx context.Context, _ string, command string, args ...string) (string, int, error) {
	return e.RunCommand(ctx, command, args...)
}

// callsByCommand returns every recorded invocation of the given binary, in
// order, as the slice of arg-slices passed to it.
func (e *poudriereMockExec) callsByCommand(command string) [][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out [][]string
	for _, c := range e.calls {
		if c.Command == command {
			out = append(out, c.Args)
		}
	}
	return out
}

// poudriereMockNode satisfies the NodeHandler interface with just the
// hostname method the reconciler reads. The other methods return
// zero-valued sensible defaults to keep the test focused.
type poudriereMockNode struct {
	hostname string
}

func (n *poudriereMockNode) Hostname() (string, error) { return n.hostname, nil }
func (n *poudriereMockNode) Reboot(_ context.Context)  {}
func (n *poudriereMockNode) Upgrade(_ context.Context) error {
	return nil
}

func (n *poudriereMockNode) Info(_ context.Context) *handler.SysInfo {
	return &handler.SysInfo{Name: n.hostname}
}

// poudriereMockSystem wires the recording exec handler and the hostname-only
// node handler into a handler.System. The remaining handler accessors are
// not invoked by the poudriere reconciler — they panic if reached so a
// future reconciler change that starts using them is caught loudly.
type poudriereMockSystem struct {
	exec *poudriereMockExec
	node *poudriereMockNode
}

func (s *poudriereMockSystem) Exec() handler.ExecHandler { return s.exec }
func (s *poudriereMockSystem) Node() handler.NodeHandler { return s.node }
func (s *poudriereMockSystem) Package() handler.PackageHandler {
	panic("poudriere reconciler unexpectedly called System.Package()")
}

func (s *poudriereMockSystem) Service() handler.ServiceHandler {
	panic("poudriere reconciler unexpectedly called System.Service()")
}

func (s *poudriereMockSystem) File() handler.FileHandler {
	panic("poudriere reconciler unexpectedly called System.File()")
}

var _ handler.System = (*poudriereMockSystem)(nil)

// poudriereTestSeq makes hostnames unique across test invocations so each
// It block creates its own ManagedNode without colliding with others.
var poudriereTestSeq int

func nextPoudriereHostname() string {
	poudriereTestSeq++
	return fmt.Sprintf("poud-test-%d", poudriereTestSeq)
}

// newPoudriereTestReconciler wires a PoudriereReconciler with the mock
// system (so exec calls are recorded) and the envtest k8sClient/scheme.
// The Command-mode runner uses the real cmdrunner with the envtest
// client so SecretEnv resolution works in tests; Command-mode tests
// use real /bin/sh programs as Dispatch / Status executables.
func newPoudriereTestReconciler(hostname string) (*PoudriereReconciler, *poudriereMockExec) {
	exec := &poudriereMockExec{}
	sys := &poudriereMockSystem{
		exec: exec,
		node: &poudriereMockNode{hostname: hostname},
	}
	logger := slog.Default().With("controller", "poudriere-test")
	r := &PoudriereReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
		tracer: otel.Tracer("test"),
		logger: logger,
		system: sys,
		runner: cmdrunner.New(k8sClient, logger.With("component", "cmdrunner")),
	}
	return r, exec
}

var _ = Describe("Poudriere Controller", func() {
	const namespace = "default"
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns early when no ManagedNode exists for this host", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)

		// No ManagedNode created — controller should bail without touching exec.
		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "anything"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(exec.calls).To(BeEmpty(), "no exec calls expected when ManagedNode is absent")
	})

	It("returns early when ManagedNode lacks the poudriere label", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{}, // no poudriere label
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "anything"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(exec.calls).To(BeEmpty(), "no exec calls expected when label gate fails")
	})

	It("returns early when poudriere label is set to disabled", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "disabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "anything"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(exec.calls).To(BeEmpty(), "disabled label must short-circuit before any exec")
	})

	It("creates trees, jails, runs portshaker and bulk when enabled", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)

		// Two `poudriere -l` calls are made (ports, jail). Stage empty
		// stdout for both so the controller treats them as not-yet-created
		// and proceeds to call -c.
		exec.outputs = []string{"", ""}

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		ports := &freebsdv1.PoudrierePorts{
			ObjectMeta: metav1.ObjectMeta{Name: "personal", Namespace: namespace},
			Spec: freebsdv1.PoudrierePortsSpec{
				FetchMethod: "git",
				Branch:      "main",
			},
		}
		Expect(k8sClient.Create(ctx, ports)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), ports) })

		bjail := &freebsdv1.PoudriereJail{
			ObjectMeta: metav1.ObjectMeta{Name: "14amd64", Namespace: namespace},
			Spec: freebsdv1.PoudriereJailSpec{
				Version:      "14.2-RELEASE",
				Architecture: "amd64",
			},
		}
		Expect(k8sClient.Create(ctx, bjail)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bjail) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "personal-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree:  "personal",
				Jail:  "14amd64",
				Ports: []string{"net/curl", "shells/zsh"},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "personal-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		poudriereCalls := exec.callsByCommand("/usr/local/bin/poudriere")
		Expect(poudriereCalls).To(ContainElement([]string{"ports", "-l"}))
		Expect(poudriereCalls).To(ContainElement(
			[]string{"ports", "-c", "-p", "personal", "-m", "git"},
		))
		Expect(poudriereCalls).To(ContainElement([]string{"jail", "-l"}))
		Expect(poudriereCalls).To(ContainElement(
			[]string{"jail", "-c", "-j", "14amd64", "-v", "14.2-RELEASE", "-m", "http"},
		))
		Expect(poudriereCalls).To(ContainElement(
			[]string{"bulk", "-p", "personal", "-j", "14amd64", "-J", "2", "net/curl", "shells/zsh"},
		))

		portshakerCalls := exec.callsByCommand("/usr/local/bin/portshaker")
		Expect(portshakerCalls).To(HaveLen(1))
		Expect(portshakerCalls[0]).To(Equal([]string{"-v"}))
	})

	It("builds only the PoudriereBulk named in the request, not other bulks in the namespace", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)
		// ports -l, jail -l = 2 List calls
		exec.outputs = []string{"", ""}

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		bjail := &freebsdv1.PoudriereJail{
			ObjectMeta: metav1.ObjectMeta{Name: "14amd64-multi", Namespace: namespace},
			Spec:       freebsdv1.PoudriereJailSpec{Version: "14.2-RELEASE"},
		}
		Expect(k8sClient.Create(ctx, bjail)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bjail) })

		ports := &freebsdv1.PoudrierePorts{
			ObjectMeta: metav1.ObjectMeta{Name: "tree-multi", Namespace: namespace},
			Spec:       freebsdv1.PoudrierePortsSpec{FetchMethod: "git"},
		}
		Expect(k8sClient.Create(ctx, ports)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), ports) })

		bulkA := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "bulk-a", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "tree-multi", Jail: "14amd64-multi", Ports: []string{"net/curl"},
			},
		}
		Expect(k8sClient.Create(ctx, bulkA)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulkA) })

		bulkB := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "bulk-b", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "tree-multi", Jail: "14amd64-multi", Ports: []string{"shells/zsh"},
			},
		}
		Expect(k8sClient.Create(ctx, bulkB)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulkB) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "bulk-a"},
		})
		Expect(err).NotTo(HaveOccurred())

		// Only bulk-a's port should appear in a `poudriere bulk` call. bulk-b
		// has its own reconcile request and would build on its own; mixing
		// them in one reconcile would prevent per-bulk ReconcilePeriod and
		// per-bulk status writes from working correctly.
		poudriereCalls := exec.callsByCommand("/usr/local/bin/poudriere")
		Expect(poudriereCalls).To(ContainElement(
			[]string{"bulk", "-p", "tree-multi", "-j", "14amd64-multi", "-J", "2", "net/curl"},
		))
		Expect(poudriereCalls).NotTo(ContainElement(
			[]string{"bulk", "-p", "tree-multi", "-j", "14amd64-multi", "-J", "2", "shells/zsh"},
		))
	})

	It("returns RequeueAfter when ReconcilePeriod is set on the bulk", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)
		exec.outputs = []string{"", ""}

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		ports := &freebsdv1.PoudrierePorts{
			ObjectMeta: metav1.ObjectMeta{Name: "periodic-tree", Namespace: namespace},
			Spec:       freebsdv1.PoudrierePortsSpec{FetchMethod: "git"},
		}
		Expect(k8sClient.Create(ctx, ports)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), ports) })

		bjail := &freebsdv1.PoudriereJail{
			ObjectMeta: metav1.ObjectMeta{Name: "periodic-jail", Namespace: namespace},
			Spec:       freebsdv1.PoudriereJailSpec{Version: "14.2-RELEASE"},
		}
		Expect(k8sClient.Create(ctx, bjail)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bjail) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "periodic-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree:            "periodic-tree",
				Jail:            "periodic-jail",
				Ports:           []string{"net/curl"},
				ReconcilePeriod: "30m",
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "periodic-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Minute))
	})

	It("writes Succeeded status with LastBuildTime after a successful build", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)
		exec.outputs = []string{"", ""}

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		ports := &freebsdv1.PoudrierePorts{
			ObjectMeta: metav1.ObjectMeta{Name: "status-tree", Namespace: namespace},
			Spec:       freebsdv1.PoudrierePortsSpec{FetchMethod: "git"},
		}
		Expect(k8sClient.Create(ctx, ports)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), ports) })

		bjail := &freebsdv1.PoudriereJail{
			ObjectMeta: metav1.ObjectMeta{Name: "status-jail", Namespace: namespace},
			Spec:       freebsdv1.PoudriereJailSpec{Version: "14.2-RELEASE"},
		}
		Expect(k8sClient.Create(ctx, bjail)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bjail) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "status-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "status-tree", Jail: "status-jail", Ports: []string{"net/curl"},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "status-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		var got freebsdv1.PoudriereBulk
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Namespace: namespace, Name: "status-bulk",
		}, &got)).To(Succeed())

		Expect(got.Status.LastBuildResult).To(Equal("Succeeded"))
		Expect(got.Status.LastError).To(BeEmpty())
		Expect(got.Status.LastBuildTime).NotTo(BeNil())
		Expect(got.Status.Conditions).To(ContainElement(And(
			HaveField("Type", condAvailable),
			HaveField("Status", metav1.ConditionTrue),
		)))
		Expect(got.Status.Conditions).To(ContainElement(And(
			HaveField("Type", condProgressing),
			HaveField("Status", metav1.ConditionFalse),
		)))
	})

	// ----------------------------------------------------------------------
	// InProcess skip-if-unchanged
	// ----------------------------------------------------------------------

	It("skips the bulk run when inputs are unchanged since the last successful build", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)
		// Stage outputs for the FIRST reconcile only; if the second
		// reconcile actually invokes poudriere we'll see additional
		// recorded calls.
		exec.outputs = []string{"", ""}

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		ports := &freebsdv1.PoudrierePorts{
			ObjectMeta: metav1.ObjectMeta{Name: "skip-tree", Namespace: namespace},
			Spec:       freebsdv1.PoudrierePortsSpec{FetchMethod: "git"},
		}
		Expect(k8sClient.Create(ctx, ports)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), ports) })

		bjail := &freebsdv1.PoudriereJail{
			ObjectMeta: metav1.ObjectMeta{Name: "skip-jail", Namespace: namespace},
			Spec:       freebsdv1.PoudriereJailSpec{Version: "14.2-RELEASE"},
		}
		Expect(k8sClient.Create(ctx, bjail)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bjail) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "skip-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "skip-tree", Jail: "skip-jail", Ports: []string{"net/curl"},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		// First reconcile: actual build runs, status is populated.
		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "skip-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		callsAfterFirst := len(exec.callsByCommand("/usr/local/bin/poudriere"))
		Expect(callsAfterFirst).To(BeNumerically(">", 0), "first reconcile must invoke poudriere")

		// Second reconcile: same spec, last build Succeeded, hash matches → skip.
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "skip-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		callsAfterSecond := len(exec.callsByCommand("/usr/local/bin/poudriere"))
		Expect(callsAfterSecond).To(Equal(callsAfterFirst),
			"second reconcile must skip the bulk: no additional poudriere invocations")
	})

	It("rebuilds when ports list changes (input hash invalidated)", func() {
		hostname := nextPoudriereHostname()
		r, exec := newPoudriereTestReconciler(hostname)
		exec.outputs = []string{"", "", "", ""} // first + second reconcile each see 2 List calls

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		ports := &freebsdv1.PoudrierePorts{
			ObjectMeta: metav1.ObjectMeta{Name: "rebuild-tree", Namespace: namespace},
			Spec:       freebsdv1.PoudrierePortsSpec{FetchMethod: "git"},
		}
		Expect(k8sClient.Create(ctx, ports)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), ports) })

		bjail := &freebsdv1.PoudriereJail{
			ObjectMeta: metav1.ObjectMeta{Name: "rebuild-jail", Namespace: namespace},
			Spec:       freebsdv1.PoudriereJailSpec{Version: "14.2-RELEASE"},
		}
		Expect(k8sClient.Create(ctx, bjail)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bjail) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "rebuild-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "rebuild-tree", Jail: "rebuild-jail", Ports: []string{"net/curl"},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		// First reconcile: builds with ["net/curl"].
		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "rebuild-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())
		callsAfterFirst := len(exec.callsByCommand("/usr/local/bin/poudriere"))

		// Edit the ports list; hash invalidates.
		var fresh freebsdv1.PoudriereBulk
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "rebuild-bulk"}, &fresh)).To(Succeed())
		fresh.Spec.Ports = []string{"net/curl", "shells/zsh"}
		Expect(k8sClient.Update(ctx, &fresh)).To(Succeed())

		// Second reconcile: hash differs → bulk runs again with the new port list.
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "rebuild-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())
		callsAfterSecond := len(exec.callsByCommand("/usr/local/bin/poudriere"))
		Expect(callsAfterSecond).To(BeNumerically(">", callsAfterFirst),
			"changed spec must invalidate the input hash and trigger a fresh build")

		// And the last bulk invocation must contain the new port.
		bulkCalls := exec.callsByCommand("/usr/local/bin/poudriere")
		var lastBulk []string
		for _, args := range bulkCalls {
			if len(args) > 0 && args[0] == "bulk" {
				lastBulk = args
			}
		}
		Expect(lastBulk).To(ContainElement("shells/zsh"))
	})

	// ----------------------------------------------------------------------
	// Command executor — dispatch / status / state machine
	// ----------------------------------------------------------------------

	It("dispatches a Command executor and records run ID, run URL after status poll", func() {
		hostname := nextPoudriereHostname()
		r, _ := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		// Dispatch program: read JSON spec on stdin, print a fixed run ID.
		// Status program: read run ID on stdin, return success JSON.
		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "cmd-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "cmd-tree", Jail: "cmd-jail", Ports: []string{"net/curl"},
				Executor: &freebsdv1.BulkExecutor{
					Type: freebsdv1.BulkExecutorCommand,
					Command: &freebsdv1.CommandExecutor{
						Dispatch: []string{"/bin/sh", "-c", `cat >/dev/null; echo run-9001`},
						Status: []string{"/bin/sh", "-c",
							`cat >/dev/null; printf '{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkRunStatus","state":"success","url":"https://example/run/9001"}'`,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		// First reconcile: dispatch — runs Dispatch program, captures run ID,
		// requeues with statusPollInterval to start polling.
		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(statusPollInterval))

		var got freebsdv1.PoudriereBulk
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cmd-bulk"}, &got)).To(Succeed())
		Expect(got.Status.LastDispatchedRunID).To(Equal("run-9001"))
		Expect(got.Status.LastDispatchTime).NotTo(BeNil())
		Expect(got.Status.LastBuildResult).To(BeEmpty(), "in-flight: terminal result not yet known")

		// Second reconcile: poll — Status program returns success, terminal state recorded.
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cmd-bulk"}, &got)).To(Succeed())
		Expect(got.Status.LastBuildResult).To(Equal("Succeeded"))
		Expect(got.Status.LastDispatchedRunURL).To(Equal("https://example/run/9001"))
		Expect(got.Status.LastDispatchedRunID).To(BeEmpty(), "terminal: in-flight marker cleared")
	})

	It("Command executor: dispatch program exit non-zero records BuildFailed", func() {
		hostname := nextPoudriereHostname()
		r, _ := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "cmd-fail-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "t", Jail: "j", Ports: []string{"net/curl"},
				Executor: &freebsdv1.BulkExecutor{
					Type: freebsdv1.BulkExecutorCommand,
					Command: &freebsdv1.CommandExecutor{
						Dispatch: []string{"/bin/sh", "-c", `cat >/dev/null; echo "fjord broke" 1>&2; exit 7`},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-fail-bulk"},
		})
		Expect(err).NotTo(HaveOccurred(), "dispatch program exit non-zero is recorded as build failure, not surfaced as Reconcile error")

		var got freebsdv1.PoudriereBulk
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cmd-fail-bulk"}, &got)).To(Succeed())
		Expect(got.Status.LastBuildResult).To(Equal("Failed"))
		Expect(got.Status.LastError).To(ContainSubstring("exit 7"))
		Expect(got.Status.LastError).To(ContainSubstring("fjord broke"))
	})

	It("Command executor without Status program: dispatch is fire-and-forget", func() {
		hostname := nextPoudriereHostname()
		r, _ := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "cmd-fnf-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "t", Jail: "j", Ports: []string{"net/curl"},
				Executor: &freebsdv1.BulkExecutor{
					Type: freebsdv1.BulkExecutorCommand,
					Command: &freebsdv1.CommandExecutor{
						Dispatch: []string{"/bin/sh", "-c", `cat >/dev/null; echo just-a-handle`},
						// No Status program — fire-and-forget.
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-fnf-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		var got freebsdv1.PoudriereBulk
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cmd-fnf-bulk"}, &got)).To(Succeed())
		Expect(got.Status.LastBuildResult).To(Equal("Succeeded"),
			"fire-and-forget records dispatch as Succeeded immediately")
		Expect(got.Status.LastDispatchedRunID).To(BeEmpty(),
			"fire-and-forget clears the in-flight marker so the next reconcile dispatches fresh")
	})

	It("Command executor: in-flight run polled across multiple reconciles", func() {
		hostname := nextPoudriereHostname()
		r, _ := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		// Status program returns "running" the first time it's invoked
		// (we simulate this by writing a counter to a tmpfile and
		// changing the response based on its value).
		tmpDir := GinkgoT().TempDir()
		statusScript := tmpDir + "/status.sh"
		Expect(os.WriteFile(statusScript, []byte(`#!/bin/sh
COUNTER=`+tmpDir+`/counter
N=$(cat "$COUNTER" 2>/dev/null || echo 0)
N=$((N + 1))
echo "$N" > "$COUNTER"
cat >/dev/null
if [ "$N" = "1" ]; then
  printf '{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkRunStatus","state":"running","url":"https://x/123"}'
else
  printf '{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkRunStatus","state":"success","url":"https://x/123"}'
fi
`), 0o755)).To(Succeed())

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "cmd-inflight-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "t", Jail: "j", Ports: []string{"net/curl"},
				Executor: &freebsdv1.BulkExecutor{
					Type: freebsdv1.BulkExecutorCommand,
					Command: &freebsdv1.CommandExecutor{
						Dispatch: []string{"/bin/sh", "-c", `cat >/dev/null; echo run-INFLIGHT`},
						Status:   []string{statusScript},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		// 1) Dispatch.
		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-inflight-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(statusPollInterval))

		// 2) Poll — running, URL recorded, still in-flight.
		result, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-inflight-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(statusPollInterval))

		var got freebsdv1.PoudriereBulk
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cmd-inflight-bulk"}, &got)).To(Succeed())
		Expect(got.Status.LastDispatchedRunID).To(Equal("run-INFLIGHT"), "in-flight marker still set while running")
		Expect(got.Status.LastDispatchedRunURL).To(Equal("https://x/123"), "URL surfaced from running state")
		Expect(got.Status.LastBuildResult).To(BeEmpty(), "no terminal result yet")

		// 3) Poll — success, terminal recorded, in-flight marker cleared.
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-inflight-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cmd-inflight-bulk"}, &got)).To(Succeed())
		Expect(got.Status.LastBuildResult).To(Equal("Succeeded"))
		Expect(got.Status.LastDispatchedRunID).To(BeEmpty())
	})

	It("Command executor: invalid spec (Type=Command without .Command) records InvalidExecutor", func() {
		hostname := nextPoudriereHostname()
		r, _ := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "cmd-invalid-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree: "t", Jail: "j", Ports: []string{"net/curl"},
				Executor: &freebsdv1.BulkExecutor{
					Type: freebsdv1.BulkExecutorCommand,
					// Command field deliberately nil
				},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-invalid-bulk"},
		})
		Expect(err).NotTo(HaveOccurred(), "validation failure recorded on status, not returned")

		var got freebsdv1.PoudriereBulk
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cmd-invalid-bulk"}, &got)).To(Succeed())
		Expect(got.Status.Conditions).To(ContainElement(And(
			HaveField("Type", condDegraded),
			HaveField("Status", metav1.ConditionTrue),
			HaveField("Reason", "InvalidExecutor"),
		)))
	})

	It("Command executor delivers BulkDispatchInput JSON on stdin per the v1 contract", func() {
		hostname := nextPoudriereHostname()
		r, _ := newPoudriereTestReconciler(hostname)

		node := &commonv1.ManagedNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				Labels:    map[string]string{labels.PoudriereBuild: "enabled"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

		// Dispatch program writes received stdin to a tmpfile we can inspect.
		tmpDir := GinkgoT().TempDir()
		stdinCapture := tmpDir + "/stdin.json"
		dispatchScript := tmpDir + "/dispatch.sh"
		Expect(os.WriteFile(dispatchScript, []byte(
			`#!/bin/sh
cat > `+stdinCapture+`
echo run-CONTRACT
`), 0o755)).To(Succeed())

		bulk := &freebsdv1.PoudriereBulk{
			ObjectMeta: metav1.ObjectMeta{Name: "cmd-contract-bulk", Namespace: namespace},
			Spec: freebsdv1.PoudriereBulkSpec{
				Tree:  "contract-tree",
				Jail:  "contract-jail",
				Ports: []string{"net/curl", "shells/zsh"},
				Executor: &freebsdv1.BulkExecutor{
					Type: freebsdv1.BulkExecutorCommand,
					Command: &freebsdv1.CommandExecutor{
						Dispatch: []string{dispatchScript},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bulk)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), bulk) })

		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: "cmd-contract-bulk"},
		})
		Expect(err).NotTo(HaveOccurred())

		// Verify the captured stdin matches the documented contract shape.
		stdin, err := os.ReadFile(stdinCapture)
		Expect(err).NotTo(HaveOccurred())

		var input cmdrunner.BulkDispatchInput
		Expect(json.Unmarshal(stdin, &input)).To(Succeed())
		Expect(input.APIVersion).To(Equal(cmdrunner.ContractAPIVersion))
		Expect(input.Kind).To(Equal(cmdrunner.KindBulkDispatchInput))
		Expect(input.Metadata.Name).To(Equal("cmd-contract-bulk"))
		Expect(input.Metadata.Namespace).To(Equal(namespace))
		Expect(input.Spec.Jail).To(Equal("contract-jail"))
		Expect(input.Spec.Tree).To(Equal("contract-tree"))
		Expect(input.Spec.Ports).To(Equal([]string{"net/curl", "shells/zsh"}))
	})
})
