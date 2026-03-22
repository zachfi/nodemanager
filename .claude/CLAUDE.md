# nodemanager

A controller that uses Kubernetes CRDs as a unified configuration source to manage OS packages, files, and services on any node — not just Kubernetes nodes. The controller can run in-cluster or off-cluster (directly on a bare-metal or VM host), treating Kubernetes as the configuration plane rather than a container orchestration target.

## Module

`github.com/zachfi/nodemanager` — Go 1.24, built with kubebuilder / controller-runtime v0.22.

## Build & Test

```sh
make build          # → bin/manager
make test           # unit tests via envtest
make test-e2e       # e2e against Kind cluster
make lint           # golangci-lint
make lint-fix       # auto-fix lint issues
make fmt            # gofmt
make manifests      # regenerate CRD/RBAC manifests
make generate       # regenerate DeepCopy methods
make run            # run controller locally
```

## Key Packages

| Path | Purpose |
|---|---|
| `api/common/v1/` | ManagedNode and ConfigSet CRD types |
| `api/freebsd/v1/` | FreeBSD Poudriere CRD types |
| `internal/controller/common/` | ManagedNode and ConfigSet reconcilers |
| `internal/controller/freebsd/` | Poudriere reconciler |
| `pkg/handler/` | Abstract interfaces (System, PackageHandler, ServiceHandler, FileHandler, ExecHandler, NodeHandler) |
| `pkg/system/` | OS detection factory — instantiates Arch, Alpine, or FreeBSD impl |
| `pkg/os/arch/`, `pkg/os/alpine/`, `pkg/os/freebsd/` | OS-specific implementations |
| `pkg/packages/` | pacman / apk / pkgng wrappers |
| `pkg/services/` | systemd / rc.d service management |
| `pkg/files/` | File management with SHA256 change detection and template expansion |
| `pkg/execs/` | Subprocess execution |
| `pkg/locker/` | Lease-based distributed locking for upgrade groups |
| `pkg/common/` | Label management, annotations, node label matching |
| `cmd/` | Entry point (main.go) and config parsing (config.go) |

## CRD API Groups

### `common.nodemanager`
- **ManagedNode** — Represents any managed host (not necessarily a Kubernetes node). Created/updated by the controller for the local hostname. Manages node labels (OS, arch) and scheduled upgrades (cron + group locking).
- **ConfigSet** — Declares desired state: `files[]`, `packages[]`, `services[]`, `executions[]`. Matched to nodes via label selectors.

### `freebsd.nodemanager`
- **PoudriereJail**, **PoudrierePorts**, **PoudriereBulk** — FreeBSD Poudriere build infrastructure.

## Architecture Patterns

- **Strategy pattern**: `pkg/handler/system.go` defines interfaces; `pkg/system/system.go` detects OS and instantiates the correct implementation.
- **Reconciliation**: Standard controller-runtime pattern. `ManagedNodeReconciler` filters by hostname (only processes its own node's ManagedNode). `ConfigSetReconciler` applies desired state from matching ConfigSets.
- **File subscriptions**: Services/execs can subscribe to files; changes trigger restarts/re-execution.
- **Distributed locking**: Lease-based (`pkg/locker/`) to prevent concurrent upgrades within an upgrade group.
- **Observability**: OpenTelemetry tracing + slog structured logging + Prometheus metrics.

## Supported Platforms

- **Arch Linux** — pacman + systemd
- **Alpine Linux** — apk + systemd
- **FreeBSD** — pkg + rc.d + poudriere

Build targets: linux/amd64, linux/arm64, linux/arm, freebsd/amd64, freebsd/arm64.

## Testing

Uses Ginkgo v2 + Gomega with envtest (embedded k8s API server). CRDs loaded from `config/crd/bases/`. Test files primarily in `internal/controller/`.

## Config Samples

Example CRs in `config/samples/`. Key pattern — a ConfigSet selects nodes by labels and declares files/packages/services declaratively.

## Linting

`.golangci.yml` — 5 min deadline. Enabled: errcheck, goconst, gocyclo, gofmt, goimports, staticcheck, unused, and others. `api/*` and `internal/*` have relaxed `lll`/`dupl` rules.
