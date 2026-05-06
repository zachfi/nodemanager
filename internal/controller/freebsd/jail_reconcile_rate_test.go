package freebsd

import (
	"context"
	"fmt"
	"log/slog"
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

// mockJailManager satisfies jail.Manager with no-op implementations.  It
// records EnsureJail calls so tests can assert on reconcile frequency.
type mockJailManager struct {
	ensureCalls atomic.Int32
	isRunning   bool
}

func (m *mockJailManager) EnsureJail(_ context.Context, _ freebsdv1.Jail) error {
	m.ensureCalls.Add(1)
	return nil
}
func (m *mockJailManager) DeleteJail(_ context.Context, _ freebsdv1.Jail) error  { return nil }
func (m *mockJailManager) StartJail(_ context.Context, _ freebsdv1.Jail) error   { return nil }
func (m *mockJailManager) StopJail(_ context.Context, _ string) error            { return nil }
func (m *mockJailManager) RestartJail(_ context.Context, _ freebsdv1.Jail) error { return nil }
func (m *mockJailManager) IsRunning(_ context.Context, _ string) (bool, error) {
	return m.isRunning, nil
}
func (m *mockJailManager) InstalledRelease(_ string) (string, error)    { return "14.2-RELEASE", nil }
func (m *mockJailManager) UpdateJail(_ context.Context, _ string) error { return nil }
func (m *mockJailManager) ExecInJail(_ context.Context, _ string, _ string, _ ...string) error {
	return nil
}
func (m *mockJailManager) BootstrapPkg(_ context.Context, _, _ string) error          { return nil }
func (m *mockJailManager) EnsureAnchor(_ context.Context, _ string, _ []string) error { return nil }
func (m *mockJailManager) FlushAnchor(_ context.Context, _ string) error              { return nil }

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
			return mock.ensureCalls.Load()
		}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 1))

		callsAfterFirst := mock.ensureCalls.Load()

		By("verifying no additional reconciles fire from status-only updates over 3 seconds")
		Consistently(func() int32 {
			return mock.ensureCalls.Load()
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
			return mock.ensureCalls.Load()
		}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 1))

		callsBefore := mock.ensureCalls.Load()

		By("updating the Jail spec (bumps generation)")
		fresh := &freebsdv1.Jail{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jailName, Namespace: namespace}, fresh)).To(Succeed())
		fresh.Spec.Release = "14.4-RELEASE"
		Expect(k8sClient.Update(ctx, fresh)).To(Succeed())

		By("verifying a new reconcile fires for the spec change")
		Eventually(func() int32 {
			return mock.ensureCalls.Load()
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
			return mock.ensureCalls.Load()
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
			return mock.ensureCalls.Load()
		}, 2*time.Second, 200*time.Millisecond).Should(Equal(int32(0)))
	})
})
