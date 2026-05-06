package freebsd

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/jail"
	"github.com/zachfi/nodemanager/pkg/locker"
)

// testControllerSeq generates unique controller names so each It block registers
// distinct Prometheus metrics and avoids "controller already exists" errors.
var testControllerSeq atomic.Int32

func nextControllerName() string {
	return fmt.Sprintf("freebsd-jail-test-%d", testControllerSeq.Add(1))
}

// execInJailCall captures the arguments to a single ExecInJail invocation.
type execInJailCall struct {
	Time    time.Time
	JailID  string
	Command string
	Args    []string
}

// bootstrapPkgCall captures arguments to BootstrapPkg.
type bootstrapPkgCall struct {
	Time     time.Time
	JailID   string
	JailRoot string
}

// anchorCall captures arguments to EnsureAnchor / FlushAnchor.
type anchorCall struct {
	Time   time.Time
	Anchor string
	Rules  []string
}

// mockJailManager satisfies jail.Manager and records every call so tests can
// assert "EnsureJail was called once with these IPs" or "StartJail was called
// before BootstrapPkg".  Recordings are per-method to keep type-safety; a
// shared mutex makes concurrent reconciles safe.
//
// Behaviour knobs (isRunning, ensureJailErr, startJailErr, ...) let a test
// drive failure paths without a real FreeBSD host.
type mockJailManager struct {
	mu sync.Mutex

	// Per-method recorded calls.  Use len(m.<field>) for count assertions
	// and access individual entries for argument assertions.
	EnsureJailCalls    []freebsdv1.Jail
	DeleteJailCalls    []freebsdv1.Jail
	StartJailCalls     []freebsdv1.Jail
	StopJailCalls      []string
	RestartJailCalls   []freebsdv1.Jail
	IsRunningCalls     []string
	UpdateJailCalls    []string
	ExecInJailCalls    []execInJailCall
	BootstrapPkgCalls  []bootstrapPkgCall
	EnsureAnchorCalls  []anchorCall
	FlushAnchorCalls   []string
	InstalledReleaseFn func(jailRoot string) (string, error) // optional override

	// Behaviour knobs.
	isRunning     bool
	ensureJailErr error
	startJailErr  error
	stopJailErr   error
	deleteJailErr error
}

// ensureCalls keeps the legacy counter API alive for older tests; new tests
// should use len(m.EnsureJailCalls).
func (m *mockJailManager) ensureCallCount() int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int32(len(m.EnsureJailCalls))
}

func (m *mockJailManager) EnsureJail(_ context.Context, j freebsdv1.Jail) error {
	m.mu.Lock()
	m.EnsureJailCalls = append(m.EnsureJailCalls, j)
	err := m.ensureJailErr
	m.mu.Unlock()
	return err
}

func (m *mockJailManager) DeleteJail(_ context.Context, j freebsdv1.Jail) error {
	m.mu.Lock()
	m.DeleteJailCalls = append(m.DeleteJailCalls, j)
	err := m.deleteJailErr
	m.mu.Unlock()
	return err
}

func (m *mockJailManager) StartJail(_ context.Context, j freebsdv1.Jail) error {
	m.mu.Lock()
	m.StartJailCalls = append(m.StartJailCalls, j)
	err := m.startJailErr
	m.mu.Unlock()
	return err
}

func (m *mockJailManager) StopJail(_ context.Context, name string) error {
	m.mu.Lock()
	m.StopJailCalls = append(m.StopJailCalls, name)
	err := m.stopJailErr
	m.mu.Unlock()
	return err
}

func (m *mockJailManager) RestartJail(_ context.Context, j freebsdv1.Jail) error {
	m.mu.Lock()
	m.RestartJailCalls = append(m.RestartJailCalls, j)
	m.mu.Unlock()
	return nil
}

func (m *mockJailManager) IsRunning(_ context.Context, name string) (bool, error) {
	m.mu.Lock()
	m.IsRunningCalls = append(m.IsRunningCalls, name)
	running := m.isRunning
	m.mu.Unlock()
	return running, nil
}

func (m *mockJailManager) InstalledRelease(jailRoot string) (string, error) {
	if m.InstalledReleaseFn != nil {
		return m.InstalledReleaseFn(jailRoot)
	}
	return "14.2-RELEASE", nil
}

func (m *mockJailManager) UpdateJail(_ context.Context, jailRoot string) error {
	m.mu.Lock()
	m.UpdateJailCalls = append(m.UpdateJailCalls, jailRoot)
	m.mu.Unlock()
	return nil
}

func (m *mockJailManager) ExecInJail(_ context.Context, jailID, command string, args ...string) error {
	m.mu.Lock()
	m.ExecInJailCalls = append(m.ExecInJailCalls, execInJailCall{
		Time: time.Now(), JailID: jailID, Command: command, Args: append([]string(nil), args...),
	})
	m.mu.Unlock()
	return nil
}

func (m *mockJailManager) BootstrapPkg(_ context.Context, jailID, jailRoot string) error {
	m.mu.Lock()
	m.BootstrapPkgCalls = append(m.BootstrapPkgCalls, bootstrapPkgCall{
		Time: time.Now(), JailID: jailID, JailRoot: jailRoot,
	})
	m.mu.Unlock()
	return nil
}

func (m *mockJailManager) EnsureAnchor(_ context.Context, anchor string, rules []string) error {
	m.mu.Lock()
	m.EnsureAnchorCalls = append(m.EnsureAnchorCalls, anchorCall{
		Time: time.Now(), Anchor: anchor, Rules: append([]string(nil), rules...),
	})
	m.mu.Unlock()
	return nil
}

func (m *mockJailManager) FlushAnchor(_ context.Context, anchor string) error {
	m.mu.Lock()
	m.FlushAnchorCalls = append(m.FlushAnchorCalls, anchor)
	m.mu.Unlock()
	return nil
}

var _ jail.Manager = (*mockJailManager)(nil)

// mockLocker is a no-op Locker for tests that never acquire or hold leases.
type mockLocker struct{}

func (l *mockLocker) Lock(_ context.Context, _ types.NamespacedName) error { return nil }
func (l *mockLocker) LockFor(_ context.Context, _ types.NamespacedName, _ time.Duration) error {
	return nil
}
func (l *mockLocker) Unlock(_ context.Context, _ types.NamespacedName) error { return nil }
func (l *mockLocker) Locked(_ context.Context, _ types.NamespacedName) bool  { return false }

var _ locker.Locker = (*mockLocker)(nil)

// newTestReconciler builds a JailReconciler wired to a mock manager and a
// synthetic hostname.  It is registered with mgr via SetupWithManager so that
// the full predicate and rate-limiter stack is exercised.  Each call uses a
// unique controllerName to avoid Prometheus metric registration conflicts when
// multiple managers are created in a single test suite run.
func newTestReconciler(mgr ctrl.Manager, hostname string, mock *mockJailManager) *JailReconciler {
	r := &JailReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		tracer:         otel.Tracer("test"),
		logger:         slog.Default().With("controller", "jail-test"),
		hostname:       hostname,
		locker:         &mockLocker{},
		manager:        mock,
		controllerName: nextControllerName(),
	}
	return r
}

var _ = Describe("Jail Controller reconcile rate", func() {
	const (
		hostname  = "test-node"
		namespace = "default"
	)

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		cancel()
	})

	It("does not loop after initial reconcile when jail is running", func() {
		By("starting a controller manager with the mock jail manager")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		mock := &mockJailManager{isRunning: true}
		reconciler := newTestReconciler(mgr, hostname, mock)
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() { _ = mgr.Start(ctx) }()

		By("creating a Jail assigned to the test hostname")
		jailName := "no-loop-test"
		j := &freebsdv1.Jail{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jailName,
				Namespace: namespace,
			},
			Spec: freebsdv1.JailSpec{
				NodeName: hostname,
				Release:  "14.2-RELEASE",
			},
		}
		Expect(k8sClient.Create(ctx, j)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), j) })

		By("waiting for the initial reconcile")
		Eventually(func() int32 {
			return mock.ensureCallCount()
		}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 1))

		callsAfterFirst := mock.ensureCallCount()

		By("verifying no additional reconciles fire from status-only updates over 3 seconds")
		Consistently(func() int32 {
			return mock.ensureCallCount()
		}, 3*time.Second, 200*time.Millisecond).Should(Equal(callsAfterFirst))
	})

	It("triggers a new reconcile when the Jail spec changes", func() {
		By("starting a controller manager")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		mock := &mockJailManager{isRunning: true}
		reconciler := newTestReconciler(mgr, hostname, mock)
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() { _ = mgr.Start(ctx) }()

		By("creating a Jail")
		jailName := "spec-change-test"
		j := &freebsdv1.Jail{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jailName,
				Namespace: namespace,
			},
			Spec: freebsdv1.JailSpec{
				NodeName: hostname,
				Release:  "14.2-RELEASE",
			},
		}
		Expect(k8sClient.Create(ctx, j)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), j) })

		By("waiting for initial reconcile")
		Eventually(func() int32 {
			return mock.ensureCallCount()
		}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 1))

		callsBefore := mock.ensureCallCount()

		By("updating the Jail spec (bumps generation)")
		fresh := &freebsdv1.Jail{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jailName, Namespace: namespace}, fresh)).To(Succeed())
		fresh.Spec.Release = "14.4-RELEASE"
		Expect(k8sClient.Update(ctx, fresh)).To(Succeed())

		By("verifying a new reconcile fires for the spec change")
		Eventually(func() int32 {
			return mock.ensureCallCount()
		}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">", callsBefore))
	})

	It("reconciles periodically when ReconcilePeriod is set", func() {
		By("starting a controller manager")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		mock := &mockJailManager{isRunning: true}
		reconciler := newTestReconciler(mgr, hostname, mock)
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() { _ = mgr.Start(ctx) }()

		By("creating a Jail with a short ReconcilePeriod")
		jailName := "periodic-test"
		j := &freebsdv1.Jail{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jailName,
				Namespace: namespace,
			},
			Spec: freebsdv1.JailSpec{
				NodeName:        hostname,
				Release:         "14.2-RELEASE",
				ReconcilePeriod: "500ms",
			},
		}
		Expect(k8sClient.Create(ctx, j)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), j) })

		By("verifying multiple reconciles happen over 2 seconds (period=500ms)")
		Eventually(func() int32 {
			return mock.ensureCallCount()
		}, 5*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 3))
	})

	It("does not reconcile jails assigned to other nodes", func() {
		By("starting a controller manager")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		mock := &mockJailManager{isRunning: true}
		reconciler := newTestReconciler(mgr, hostname, mock)
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() { _ = mgr.Start(ctx) }()

		By("creating a Jail assigned to a different node")
		jailName := "other-node-test"
		j := &freebsdv1.Jail{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jailName,
				Namespace: namespace,
			},
			Spec: freebsdv1.JailSpec{
				NodeName: "other-node",
				Release:  "14.2-RELEASE",
			},
		}
		Expect(k8sClient.Create(ctx, j)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), j) })

		By("verifying EnsureJail is never called")
		Consistently(func() int32 {
			return mock.ensureCallCount()
		}, 2*time.Second, 200*time.Millisecond).Should(Equal(int32(0)))
	})

	It("records the merged spec passed to EnsureJail", func() {
		By("starting a controller manager")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		mock := &mockJailManager{isRunning: true}
		reconciler := newTestReconciler(mgr, hostname, mock)
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() { _ = mgr.Start(ctx) }()

		By("creating a Jail with multiple IPs and a mount")
		jailName := "args-capture-test"
		j := &freebsdv1.Jail{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jailName,
				Namespace: namespace,
			},
			Spec: freebsdv1.JailSpec{
				NodeName:  hostname,
				Release:   "14.2-RELEASE",
				Interface: "lo1",
				Inets:     []string{"192.0.2.10", "192.0.2.11"},
				Inet6s:    []string{"2001:db8::10"},
				Mounts: []freebsdv1.JailMount{
					{HostPath: "/data/foo", JailPath: "/foo"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, j)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), j) })

		By("waiting for the first reconcile")
		Eventually(func() int32 {
			return mock.ensureCallCount()
		}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 1))

		By("verifying the recorded EnsureJail argument matches spec")
		mock.mu.Lock()
		defer mock.mu.Unlock()
		Expect(mock.EnsureJailCalls).NotTo(BeEmpty())
		recorded := mock.EnsureJailCalls[0]
		Expect(recorded.Name).To(Equal(jailName))
		Expect(recorded.Spec.Interface).To(Equal("lo1"))
		Expect(recorded.Spec.Inets).To(Equal([]string{"192.0.2.10", "192.0.2.11"}))
		Expect(recorded.Spec.Inet6s).To(Equal([]string{"2001:db8::10"}))
		Expect(recorded.Spec.Mounts).To(HaveLen(1))
		Expect(recorded.Spec.Mounts[0].HostPath).To(Equal("/data/foo"))

		By("verifying StartJail was NOT called because IsRunning returned true")
		Expect(mock.StartJailCalls).To(BeEmpty())

		By("verifying IsRunning was called with the jail name")
		Expect(mock.IsRunningCalls).NotTo(BeEmpty())
		Expect(mock.IsRunningCalls[0]).To(Equal(jailName))

		By("verifying BootstrapPkg was called once with the jail name")
		Expect(mock.BootstrapPkgCalls).To(HaveLen(1))
		Expect(mock.BootstrapPkgCalls[0].JailID).To(Equal(jailName))
	})

	It("calls StartJail when IsRunning returns false", func() {
		By("starting a controller manager with isRunning=false")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())

		// First IsRunning call returns false → StartJail invoked.
		// After StartJail, the controller queries IsRunning again; the mock
		// keeps returning false so we verify StartJail was attempted.
		mock := &mockJailManager{isRunning: false}
		reconciler := newTestReconciler(mgr, hostname, mock)
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() { _ = mgr.Start(ctx) }()

		jailName := "start-jail-test"
		j := &freebsdv1.Jail{
			ObjectMeta: metav1.ObjectMeta{Name: jailName, Namespace: namespace},
			Spec: freebsdv1.JailSpec{
				NodeName: hostname,
				Release:  "14.2-RELEASE",
			},
		}
		Expect(k8sClient.Create(ctx, j)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), j) })

		Eventually(func() int {
			mock.mu.Lock()
			defer mock.mu.Unlock()
			return len(mock.StartJailCalls)
		}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 1))

		By("verifying StartJail received the same jail spec as EnsureJail")
		mock.mu.Lock()
		defer mock.mu.Unlock()
		Expect(mock.StartJailCalls[0].Name).To(Equal(jailName))
		Expect(mock.EnsureJailCalls[0].Name).To(Equal(jailName))
	})
})
