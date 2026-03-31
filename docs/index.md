# nodemanager

nodemanager manages OS-level configuration — packages, files, services — using
Kubernetes CRDs as the configuration source. A small controller binary runs
directly on each host (bare-metal, VM, or container) and continuously
reconciles the host against the desired state declared in the cluster.

The key idea: **Kubernetes as the configuration plane, not the container
orchestration target.** You do not need to run workloads on Kubernetes to
benefit from it. You only need a cluster to act as the shared configuration
store, and nodemanager running on each host to apply it.

## Why nodemanager?

Most configuration management tools (Ansible, Salt, Puppet) are push-based
and require a separate control plane, inventory, and authentication model.
nodemanager reuses the Kubernetes API you likely already have, gains all of the
audit, RBAC, and GitOps properties of Kubernetes resources for free, and
eliminates the need for SSH access to push configuration.

- **Pull-based** — each node connects to the cluster and reconciles its own
  state. No push, no SSH, no agent callbacks.
- **Declarative and idempotent** — reconciliation runs continuously; the node
  always converges to the declared state.
- **Label-driven** — `ConfigSet` objects are matched to nodes via label
  selectors, the same mechanism Kubernetes uses everywhere else.
- **Off-cluster nodes are first-class** — a node does not need to be a
  Kubernetes worker. Any host that can reach the API server can be managed.
- **GitOps-ready** — `ConfigSet` and `ManagedNode` objects live in Git and are
  applied to the cluster like any other resource.

## How it works

```
┌─────────────────────────────┐
│        Kubernetes           │
│                             │
│  ConfigSet (label selector) │◄── desired state (git / kubectl)
│  ManagedNode  (per host)    │──► observed state (status)
└──────────┬──────────────────┘
           │ watch + reconcile
    ┌──────▼──────┐   ┌─────────────┐   ┌─────────────┐
    │  myhost     │   │  db01       │   │  web01      │
    │  nodemanager│   │  nodemanager│   │  nodemanager│
    └─────────────┘   └─────────────┘   └─────────────┘
```

Each running controller:

1. Creates and owns a `ManagedNode` object for its hostname.
2. Labels the `ManagedNode` with OS, architecture, and any custom labels.
3. Watches all `ConfigSet` objects whose labels match the local `ManagedNode`.
4. Reconciles packages, files, services, and executions declared in matching
   `ConfigSet`s onto the host.
5. Publishes observed state (OS release, network interfaces, SSH host keys,
   WireGuard keys) back to the `ManagedNode` status.

## Supported platforms

| OS | Packages | Services |
|---|---|---|
| Arch Linux | pacman | systemd |
| Alpine Linux | apk | systemd |
| FreeBSD | pkgng | rc.d |

Build targets: `linux/amd64`, `linux/arm64`, `linux/arm`, `freebsd/amd64`,
`freebsd/arm64`.

## Quick start

```sh
# Install CRDs into the cluster
make install

# Run the controller against the current kubeconfig context (development)
make run
```

For running nodemanager as a long-lived service on a real host, see the
[deployment guide](deployment.md).

For a practical introduction to declaring configuration, see the
[getting started guide](getting-started.md).
