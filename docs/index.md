# nodemanager

nodemanager is a controller that uses Kubernetes CRDs as a unified configuration
source to manage OS packages, files, and services on any node — not just
Kubernetes nodes. The controller can run in-cluster or off-cluster (directly on
a bare-metal or VM host), treating Kubernetes as the configuration plane rather
than a container orchestration target.

## How it works

Each running controller creates and owns a `ManagedNode` object for the local
hostname. `ConfigSet` objects declare desired state (packages, files, services,
executions) and are matched to nodes via label selectors. When a `ConfigSet`'s
labels match the local `ManagedNode`, the controller reconciles its contents
onto the node.

Files can carry template content rendered via [gomplate](https://docs.gomplate.ca/),
with data sourced from Kubernetes Secrets and ConfigMaps. Services and
executions can subscribe to file changes and will restart or re-run when a
subscribed file is modified.

Scheduled upgrades are supported via a cron expression on the `ManagedNode`,
with optional distributed locking so only one node in a group upgrades at a
time.

## Supported platforms

| OS | Packages | Services |
|---|---|---|
| Arch Linux | pacman | systemd |
| Alpine Linux | apk | systemd |
| FreeBSD | pkgng | rc.d |

Build targets: `linux/amd64`, `linux/arm64`, `linux/arm`, `freebsd/amd64`, `freebsd/arm64`.

## Prerequisites

- Go v1.24+
- kubectl v1.11.3+
- Access to a Kubernetes cluster

## Quick start

```sh
# Install CRDs
make install

# Run the controller locally against the current kubeconfig context
make run
```

See the [API reference](api/configset.md) for `ConfigSet` and `ManagedNode` field details.
