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
	"fmt"
	"log/slog"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
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
func newPoudriereTestReconciler(hostname string) (*PoudriereReconciler, *poudriereMockExec) {
	exec := &poudriereMockExec{}
	sys := &poudriereMockSystem{
		exec: exec,
		node: &poudriereMockNode{hostname: hostname},
	}
	r := &PoudriereReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
		tracer: otel.Tracer("test"),
		logger: slog.Default().With("controller", "poudriere-test"),
		system: sys,
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

	It("processes every PoudriereBulk, not just the one named in the request", func() {
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

		poudriereCalls := exec.callsByCommand("/usr/local/bin/poudriere")
		Expect(poudriereCalls).To(ContainElement(
			[]string{"bulk", "-p", "tree-multi", "-j", "14amd64-multi", "-J", "2", "net/curl"},
		))
		Expect(poudriereCalls).To(ContainElement(
			[]string{"bulk", "-p", "tree-multi", "-j", "14amd64-multi", "-J", "2", "shells/zsh"},
		))
	})
})
