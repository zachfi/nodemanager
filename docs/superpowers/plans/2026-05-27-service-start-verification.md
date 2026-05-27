# Service Start Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect daemons that exit silently after `service start` returns 0 (e.g. nslcd parse-error case), surface the failure as a distinct metric label, and alert on the start-loop pattern.

**Architecture:** Three additive changes to the ConfigSet reconciler in `internal/controller/common/`. (1) Add a `service` label to `nodemanager_service_operations_total`. (2) After each `Start`/`Restart`, sleep `ConfigSetConfig.StartVerifyDelay` then re-poll `Status`; if not Running, record `result="exited"` and log a warning. (3) Add `NodeManagerServiceStartLoop` alert in `monitoring/alerts/nodemanager.libsonnet`.

**Tech Stack:** Go 1.24, controller-runtime v0.22, prometheus/client_golang, Ginkgo v2 + Gomega, jsonnet (mixin).

**Issue:** [code.znet/znet/nodemanager#2](https://code.znet/znet/nodemanager/issues/2) — proposal 0115.

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `internal/controller/common/metrics.go` | Modify | Add `service` label to `serviceOperationsTotal`. |
| `internal/controller/common/config.go` | Modify | Add `ConfigSetConfig.StartVerifyDelay` field + flag. |
| `internal/controller/common/configset_controller.go` | Modify | Pass `svc.Name` to all 5 `serviceOperationsTotal.WithLabelValues` callsites; add post-Start/Restart verify block. |
| `internal/controller/common/configset_controller_test.go` | Modify | Three new specs: existing-label-shape regression, post-Start verify catches `Stopped`, restart-verify catches `Stopped`. |
| `monitoring/alerts/nodemanager.libsonnet` | Modify | Add `NodeManagerServiceStartLoop` alert next to `NodeManagerServiceOperationFailed`. |
| `docs/monitoring/metrics.md` | Modify | Update label set documentation for `nodemanager_service_operations_total`. |

---

## Task 1: Add `service` label to the service-operations counter

**Files:**
- Modify: `internal/controller/common/metrics.go:36-39`
- Modify: `internal/controller/common/configset_controller.go:808, 816, 838, 848, 896`
- Modify: `docs/monitoring/metrics.md:41`

This is a label-set change. It must ship together with the alert update (Task 5) so PromQL `sum by (node, service)` works. There is no existing test that asserts label-set shape; we'll add one in the next task. Land this as one mechanical commit.

- [ ] **Step 1.1: Change the metric definition to add the `service` label**

Edit `internal/controller/common/metrics.go` lines 35-39 from:

```go
	// serviceOperationsTotal counts service manager operations (start/stop/restart/enable/disable).
	serviceOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_service_operations_total",
		Help: "Total number of service manager operations.",
	}, []string{"node", "operation", "result"})
```

to:

```go
	// serviceOperationsTotal counts service manager operations (start/stop/restart/enable/disable).
	// The "service" label scopes a runaway-restart signal to the offending daemon
	// (cardinality bounded by ManagedService names per node — ~30 across the fleet).
	serviceOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_service_operations_total",
		Help: "Total number of service manager operations.",
	}, []string{"node", "service", "operation", "result"})
```

- [ ] **Step 1.2: Update all 5 callsites in `configset_controller.go` to pass `svc.Name`**

Five call sites under `handleServiceSet`. The `service` label slots between `nodeName` and `operation`:

| Line | Before | After |
|---|---|---|
| 808 | `serviceOperationsTotal.WithLabelValues(nodeName, "enable", result).Inc()` | `serviceOperationsTotal.WithLabelValues(nodeName, svc.Name, "enable", result).Inc()` |
| 816 | `serviceOperationsTotal.WithLabelValues(nodeName, "disable", result).Inc()` | `serviceOperationsTotal.WithLabelValues(nodeName, svc.Name, "disable", result).Inc()` |
| 838 | `serviceOperationsTotal.WithLabelValues(nodeName, "start", result).Inc()` | `serviceOperationsTotal.WithLabelValues(nodeName, svc.Name, "start", result).Inc()` |
| 848 | `serviceOperationsTotal.WithLabelValues(nodeName, "stop", result).Inc()` | `serviceOperationsTotal.WithLabelValues(nodeName, svc.Name, "stop", result).Inc()` |
| 896 | `serviceOperationsTotal.WithLabelValues(nodeName, "restart", result).Inc()` | `serviceOperationsTotal.WithLabelValues(nodeName, restart, "restart", result).Inc()` |

Use Edit tool per line. Note line 896 is inside the `restartF` closure where the service name is the local variable `restart`, not `svc.Name`.

- [ ] **Step 1.3: Update `docs/monitoring/metrics.md` row**

Edit line 41 from:

```
| `nodemanager_service_operations_total` | `node`, `operation`, `result` | Service manager operations. `operation` is `start`, `stop`, `restart`, `enable`, or `disable`. |
```

to:

```
| `nodemanager_service_operations_total` | `node`, `service`, `operation`, `result` | Service manager operations. `operation` is `start`, `stop`, `restart`, `enable`, or `disable`. `result` is `success`, `error`, or `exited` (start/restart only — daemon exited within `StartVerifyDelay` of the call). |
```

- [ ] **Step 1.4: Run the build to verify everything compiles**

Run: `make build`

Expected: clean build, two binaries produced under `bin/`.

- [ ] **Step 1.5: Run the existing test suite to verify nothing regressed**

Run: `make test`

Expected: pass — existing assertions on the mock service handler (e.g. `mockServiceHandler.startCalls` HaveKey) don't touch metric labels, so they still pass.

- [ ] **Step 1.6: Commit**

```bash
git add internal/controller/common/metrics.go \
        internal/controller/common/configset_controller.go \
        docs/monitoring/metrics.md
git commit -m "$(cat <<'EOF'
feat(metrics): add service label to nodemanager_service_operations_total

Bounds a runaway-restart signal to the offending daemon. Cardinality
bounded by ManagedService names per node (~30 across the fleet).

Refs znet/nodemanager#2 (proposal 0115).
EOF
)"
```

---

## Task 2: Add `StartVerifyDelay` to ConfigSetConfig

**Files:**
- Modify: `internal/controller/common/config.go:81-99`

Add the flag now so Task 3 can reference it. Default 3s catches parse-error / port-bind-error daemons that exit in <1s while leaving headroom for slow-starting daemons (heavy LDAP TLS, large state restore) that pass the verify window already.

- [ ] **Step 2.1: Add the field to `ConfigSetConfig`**

Edit `internal/controller/common/config.go` to add a new field after `ReconcilePeriod`. Replace:

```go
	// ReconcilePeriod controls how often the ConfigSet controller re-applies
	// desired state even without a Kubernetes event.  Set to match the node's
	// reconcilePeriod for consistent convergence behaviour.  Zero means
	// event-driven only.
	ReconcilePeriod time.Duration `json:"reconcilePeriod,omitempty"`
	// GomplatePath is propagated from ControllerConfig at startup; not a CLI flag.
	GomplatePath string `json:"-"`
```

with:

```go
	// ReconcilePeriod controls how often the ConfigSet controller re-applies
	// desired state even without a Kubernetes event.  Set to match the node's
	// reconcilePeriod for consistent convergence behaviour.  Zero means
	// event-driven only.
	ReconcilePeriod time.Duration `json:"reconcilePeriod,omitempty"`
	// StartVerifyDelay is how long to wait after a Start/Restart before
	// re-polling Status to confirm the daemon is still Running. Catches the
	// case where rc.d / systemctl returns 0 but the daemon self-aborts on a
	// parse error or port-bind error within ~1s. Zero disables verification.
	StartVerifyDelay time.Duration `json:"startVerifyDelay,omitempty"`
	// GomplatePath is propagated from ControllerConfig at startup; not a CLI flag.
	GomplatePath string `json:"-"`
```

- [ ] **Step 2.2: Register the flag with a 3s default**

Edit the `RegisterFlagsAndApplyDefaults` method. Replace:

```go
func (c *ConfigSetConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.FileBucket.RegisterFlagsAndApplyDefaults(prefix+".file-bucket", f)
	f.DurationVar(&c.ReconcilePeriod, prefix+".reconcile-period", 0, "How often to re-apply ConfigSets regardless of events (0 = event-driven only).")
}
```

with:

```go
func (c *ConfigSetConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.FileBucket.RegisterFlagsAndApplyDefaults(prefix+".file-bucket", f)
	f.DurationVar(&c.ReconcilePeriod, prefix+".reconcile-period", 0, "How often to re-apply ConfigSets regardless of events (0 = event-driven only).")
	f.DurationVar(&c.StartVerifyDelay, prefix+".start-verify-delay", 3*time.Second, "After Start/Restart, wait this long then re-poll Status to confirm the daemon stayed Running. Records result=\"exited\" if not. Zero disables verification.")
}
```

- [ ] **Step 2.3: Verify the build**

Run: `make build`

Expected: clean build.

- [ ] **Step 2.4: Commit**

```bash
git add internal/controller/common/config.go
git commit -m "$(cat <<'EOF'
feat(config): add ConfigSetConfig.StartVerifyDelay (default 3s)

Prep for post-Start liveness check. 3s default catches parse-error /
port-bind-error daemons that exit in <1s; slow-starting daemons are past
the window. Zero disables.

Refs znet/nodemanager#2 (proposal 0115).
EOF
)"
```

---

## Task 3: Post-Start verify in the inline-Start branch (TDD)

**Files:**
- Modify: `internal/controller/common/configset_controller.go:826-839`
- Modify: `internal/controller/common/configset_controller_test.go` (new Context block)

The inline Start happens in `handleServiceSet` when `svc.Ensure == "running"` and `status != Running`. After Start, if `r.cfg.StartVerifyDelay > 0`, sleep then re-poll Status. If not Running, record `result="exited"` instead of `success` and log a warning. The mock test asserts behavior via `mockServiceHandler.statusCalls`: in the legacy path, Status is called once (the pre-Start check at line 826); in the verify path, Status is called twice (pre + post).

- [ ] **Step 3.1: Write the failing test — post-Start verify catches a daemon that exits**

Add this new `Context` block at the end of `configset_controller_test.go`, immediately before the final `})` that closes the outer `Describe`:

```go
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
	})
```

The test file may need a `time` import. If `goimports` complains in Step 3.3, add it then. The file already imports `commonv1`, `noop`, `services`, `slog`, `os`, `context` — verify via:

```bash
grep -n "^import\|\"time\"\|\"context\"" internal/controller/common/configset_controller_test.go | head -10
```

If `"time"` is missing, add it to the existing import block.

- [ ] **Step 3.2: Run the new tests to verify they fail**

Run:

```bash
go test ./internal/controller/common/ -run TestControllers -v -ginkgo.focus="Service start verification" 2>&1 | tail -30
```

Expected: FAIL on the first spec (statusCalls is 1, not >=2 — verify path doesn't exist yet). The second spec (zero-delay) will pass because the existing code already polls Status once.

- [ ] **Step 3.3: Implement the post-Start verify in the inline-Start branch**

In `internal/controller/common/configset_controller.go`, replace the inline-Start block (current lines 829-839):

```go
		switch services.ServiceStatusFromString(svc.Ensure) {
		case services.Running:
			if status != services.Running {
				startErr := svcHandler.Start(svcCtx, svc.Name)
				result := "success"
				if startErr != nil {
					result = "error"
					errs = append(errs, fmt.Errorf("failed to start service %q: %w", svc.Name, startErr))
				}
				serviceOperationsTotal.WithLabelValues(nodeName, svc.Name, "start", result).Inc()
			}
```

with:

```go
		switch services.ServiceStatusFromString(svc.Ensure) {
		case services.Running:
			if status != services.Running {
				startErr := svcHandler.Start(svcCtx, svc.Name)
				result := "success"
				switch {
				case startErr != nil:
					result = "error"
					errs = append(errs, fmt.Errorf("failed to start service %q: %w", svc.Name, startErr))
				case r.cfg.StartVerifyDelay > 0:
					select {
					case <-time.After(r.cfg.StartVerifyDelay):
					case <-svcCtx.Done():
					}
					if postStatus, _ := svcHandler.Status(svcCtx, svc.Name); postStatus != services.Running {
						result = "exited"
						r.logger.Warn("service exited shortly after start",
							"service", svc.Name,
							"post_status", postStatus.String(),
							"verify_delay", r.cfg.StartVerifyDelay.String(),
						)
					}
				}
				serviceOperationsTotal.WithLabelValues(nodeName, svc.Name, "start", result).Inc()
			}
```

If `time` is not already imported in this file, add it. Check with:

```bash
grep -n '^\s*"time"' internal/controller/common/configset_controller.go
```

If missing, add `"time"` to the existing import block alphabetically.

- [ ] **Step 3.4: Run the new tests to verify they pass**

Run:

```bash
go test ./internal/controller/common/ -run TestControllers -v -ginkgo.focus="Service start verification" 2>&1 | tail -30
```

Expected: both specs PASS.

- [ ] **Step 3.5: Run the full test suite to confirm no regressions**

Run: `make test`

Expected: full pass.

- [ ] **Step 3.6: Commit**

```bash
git add internal/controller/common/configset_controller.go \
        internal/controller/common/configset_controller_test.go
git commit -m "$(cat <<'EOF'
feat(configset): verify service stays Running after Start

After a successful Start, sleep StartVerifyDelay and re-poll Status.
If the daemon is no longer Running, record result="exited" and log a
warning. Catches the case where rc.d / systemctl returns 0 but the
daemon self-aborts within ~1s on a config parse error.

Motivated by nslcd parse-error incident on ex1 — 5 days of silent
~57 starts/hour with no failure signal anywhere.

Refs znet/nodemanager#2 (proposal 0115 part 1, inline-Start branch).
EOF
)"
```

---

## Task 4: Post-Restart verify in `restartF` (TDD)

**Files:**
- Modify: `internal/controller/common/configset_controller.go:858-902`
- Modify: `internal/controller/common/configset_controller_test.go` (extend the verify Context)

Symmetric change to `restartF`. The `restart` is keyed off changed files, so a managed file flipping bad will fire Restart on the same broken daemon — same silent-failure pattern. Use `operation="restart"` so the metric stays distinct from start, but reuse `result="exited"` so the alert's filter catches both unmodified.

- [ ] **Step 4.1: Write the failing test — post-Restart verify catches a daemon that exits**

Append this `It(...)` inside the existing `Context("Service start verification", ...)` block added in Task 3:

```go
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
```

This test references `svc.onRestart` (a new mock hook) and `noopLocker` (a no-op locker). Both must be added before the test compiles.

- [ ] **Step 4.2: Add an `onRestart` hook to the mock and a `noopLocker`**

Edit `internal/controller/common/mock_test.go`. Find the `mockServiceHandler` struct (around line 89) and add a hook field:

```go
type mockServiceHandler struct {
	startCalls   map[string]int
	stopCalls    map[string]int
	restartCalls map[string]int
	statusCalls  map[string]int
	enableCalls  map[string]int
	disableCalls map[string]int
	reloadCalls  map[string]int
	setArgsCalls map[string]int

	// TODO: implement daemon reload calls if needed
	daemonReloadCalls int

	serviceStatus map[string]services.ServiceStatus // Simulated service status

	// onRestart, when non-nil, is invoked from Restart() before it returns.
	// Tests use this to mutate serviceStatus, simulating a daemon that exits
	// immediately after a successful Restart returns.
	onRestart func(ctx context.Context, service string)
}
```

Find `func (m *mockServiceHandler) Restart(...)` (around line 123) and replace:

```go
func (m *mockServiceHandler) Restart(ctx context.Context, service string) error {
	if m.restartCalls == nil {
		m.restartCalls = make(map[string]int)
	}
	m.restartCalls[service]++
	// Simulate restarting the service
	return nil // Return nil to indicate success
}
```

with:

```go
func (m *mockServiceHandler) Restart(ctx context.Context, service string) error {
	if m.restartCalls == nil {
		m.restartCalls = make(map[string]int)
	}
	m.restartCalls[service]++
	if m.onRestart != nil {
		m.onRestart(ctx, service)
	}
	return nil
}
```

Add a `noopLocker` at the end of `mock_test.go`:

```go
type noopLocker struct{}

func (noopLocker) Lock(_ context.Context, _ types.NamespacedName) error   { return nil }
func (noopLocker) Unlock(_ context.Context, _ types.NamespacedName) error { return nil }
```

Make sure `types` is imported in `mock_test.go`. Check with:

```bash
grep -n "k8s.io/apimachinery/pkg/types" internal/controller/common/mock_test.go
```

If missing, add `"k8s.io/apimachinery/pkg/types"` to the import block.

Verify `locker.Locker` is satisfied by these two methods only — check the interface:

```bash
grep -n "type Locker interface" pkg/locker/locker.go
```

If the interface has more methods, extend `noopLocker` to cover them (return `nil` everywhere).

- [ ] **Step 4.3: Run the new test to verify it fails**

Run:

```bash
go test ./internal/controller/common/ -run TestControllers -v -ginkgo.focus="result=exited on Restart" 2>&1 | tail -20
```

Expected: FAIL — current `restartF` doesn't poll Status after Restart, so `statusCalls["badd"]` is 1 (only the pre-loop check), not 2.

- [ ] **Step 4.4: Implement the post-Restart verify in `restartF`**

In `internal/controller/common/configset_controller.go`, replace the tail of `restartF` (current lines 888-901):

```go
		r.logger.Info("restarting service", "name", restart, "triggering_files", restartSvc.triggeringFiles)
		restartHandler := withUserContext(handler, restartCtx)
		err = restartHandler.Restart(restartCtx, restart)
		result := "success"
		if err != nil {
			result = "error"
			restartSpan.SetStatus(codes.Error, err.Error())
		}
		serviceOperationsTotal.WithLabelValues(nodeName, restart, "restart", result).Inc()
		if err != nil {
			return fmt.Errorf("failed to restart service %q: %w", restart, err)
		}

		return nil
	}
```

with:

```go
		r.logger.Info("restarting service", "name", restart, "triggering_files", restartSvc.triggeringFiles)
		restartHandler := withUserContext(handler, restartCtx)
		err = restartHandler.Restart(restartCtx, restart)
		result := "success"
		switch {
		case err != nil:
			result = "error"
			restartSpan.SetStatus(codes.Error, err.Error())
		case r.cfg.StartVerifyDelay > 0:
			select {
			case <-time.After(r.cfg.StartVerifyDelay):
			case <-restartCtx.Done():
			}
			if postStatus, _ := restartHandler.Status(restartCtx, restart); postStatus != services.Running {
				result = "exited"
				r.logger.Warn("service exited shortly after restart",
					"service", restart,
					"post_status", postStatus.String(),
					"verify_delay", r.cfg.StartVerifyDelay.String(),
				)
			}
		}
		serviceOperationsTotal.WithLabelValues(nodeName, restart, "restart", result).Inc()
		if err != nil {
			return fmt.Errorf("failed to restart service %q: %w", restart, err)
		}

		return nil
	}
```

- [ ] **Step 4.5: Run the new test to verify it passes**

Run:

```bash
go test ./internal/controller/common/ -run TestControllers -v -ginkgo.focus="result=exited on Restart" 2>&1 | tail -20
```

Expected: PASS.

- [ ] **Step 4.6: Run the full test suite**

Run: `make test`

Expected: full pass.

- [ ] **Step 4.7: Commit**

```bash
git add internal/controller/common/configset_controller.go \
        internal/controller/common/configset_controller_test.go \
        internal/controller/common/mock_test.go
git commit -m "$(cat <<'EOF'
feat(configset): verify service stays Running after Restart

Symmetric to the Start path. A managed file going bad triggers Restart
via SubscribeFiles; without this check the same silent-exit pattern
would still ratchet result=success forever.

Refs znet/nodemanager#2 (proposal 0115 part 1, restartF closure).
EOF
)"
```

---

## Task 5: NodeManagerServiceStartLoop alert

**Files:**
- Modify: `monitoring/alerts/nodemanager.libsonnet:213` (insert after the `NodeManagerServiceOperationFailed` alert)

The disjunction handles two regimes: (1) the clean post-Part-1 signal `result="exited"`; (2) a fallback for transitional / Linux paths where the wrapper genuinely returns 0 on a fast-dying daemon and the status re-poll happens to catch it Running — a sustained high start rate is itself the signal.

- [ ] **Step 5.1: Insert the new alert block**

Edit `monitoring/alerts/nodemanager.libsonnet`. Find the `NodeManagerServiceOperationFailed` block (lines 199-213). Immediately after its closing `},` (line 213), insert:

```jsonnet
    {
      alert: 'NodeManagerServiceStartLoop',
      expr: |||
        sum by (node, service) (rate(nodemanager_service_operations_total{result="exited"}[10m])) > 0
        or
        sum by (node, service) (rate(nodemanager_service_operations_total{operation="start"}[10m])) > (0.5/60)
      |||,
      'for': '15m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'nodemanager keeps (re)starting {{ $labels.service }} on {{ $labels.node }}.',
        description: |||
          {{ $labels.service }} on {{ $labels.node }} either exits shortly
          after start (result="exited"), or has been started more than
          0.5/min for 15 minutes. Almost always a broken config file —
          inspect the service's recent stdout/journal and the
          nodemanager controller logs for "service exited shortly after"
          warnings.
        |||,
      },
    },
```

- [ ] **Step 5.2: Lint the libsonnet**

Run:

```bash
jsonnet -J monitoring monitoring/render.jsonnet > /tmp/rendered-alerts.json && jq '.groups[0].rules[] | select(.alert == "NodeManagerServiceStartLoop")' /tmp/rendered-alerts.json
```

Expected: jsonnet emits valid JSON with no errors, jq returns the new alert object containing `alert`, `expr`, `for`, `labels`, `annotations`. If `jsonnet` is not on PATH, fall back to:

```bash
test -f bin/jsonnet && ./bin/jsonnet -J monitoring monitoring/render.jsonnet | jq '.groups[0].rules[] | select(.alert == "NodeManagerServiceStartLoop")'
```

- [ ] **Step 5.3: Commit**

```bash
git add monitoring/alerts/nodemanager.libsonnet
git commit -m "$(cat <<'EOF'
feat(alerts): NodeManagerServiceStartLoop on restart loops

Fires when a service emits result="exited" or sustains a start rate
above 0.5/min for 15 minutes — the runaway-restart pattern that hid
nslcd's silent failure for 5 days on ex1.

Refs znet/nodemanager#2 (proposal 0115 part 3).
EOF
)"
```

---

## Task 6: Verify end-to-end and push

**Files:** none

- [ ] **Step 6.1: Lint, full build, full test**

Run:

```bash
make lint && make build && make test
```

Expected: all three pass.

- [ ] **Step 6.2: Inspect the git log**

Run: `git log --oneline -5`

Expected: five commits in order — `feat(metrics)`, `feat(config)`, `feat(configset) ... Start`, `feat(configset) ... Restart`, `feat(alerts)`.

- [ ] **Step 6.3: Open a PR (manual; requires user confirmation per CLAUDE.md)**

Push the branch and open the PR via `fj -H code.znet pr create` referencing issue #2. Stop and confirm with the user before pushing.

---

## Self-Review Notes

- **Spec coverage:** Issue parts 1-3 are covered by Tasks 3+4 (Part 1), Task 1 (Part 2), Task 5 (Part 3). Task 2 is required scaffolding for Part 1.
- **Restart vs Start semantics:** decided in conversation — `operation="restart"` distinct, `result="exited"` shared. The alert filters on `result="exited"` without naming the operation, so it catches both.
- **Cardinality:** ~30 services × ~10 nodes × 3 ops × 3 results = ~2700 active series ceiling. Safe.
- **Test approach:** asserts behavior via `mockServiceHandler.statusCalls` count, which is sufficient to prove the post-call poll happened. Asserting the literal metric label would require vendoring `prometheus/client_golang/prometheus/testutil` (not currently vendored) — skipped as out of scope; the alert provides the operational signal.
- **Default 3s:** always-on. Tests with zero-value `cfg` skip the verify path (existing behavior preserved); new tests set 5ms explicitly.
- **Out of scope (per issue):** exponential back-off, journal capture, systemd notify integration.
