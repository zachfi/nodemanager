# Offline apply

`nodemanager apply` applies a directory of `ConfigSet` manifests to the local
host without connecting to a Kubernetes cluster. It is useful for:

- **Machines that cannot reach the cluster** — work laptops, travel machines,
  air-gapped systems.
- **Bootstrap** — configuring a new host before cluster access is established.
- **Dotfiles / homedir management** — managing user configuration files with
  the same model used for system configuration, from a standalone `homedir`
  repository that does not require a running cluster.

The apply path reuses the same OS-level machinery as the controller (package
manager, service manager, file writer) so behaviour is identical.

## Usage

```sh
nodemanager apply --manifest-dir ./manifests/
```

| Flag | Default | Description |
|---|---|---|
| `--manifest-dir` | *(required)* | Directory containing `ConfigSet` (and optionally `ManagedNode`) YAML files. |
| `--node-name` | `os.Hostname()` | Override the node name used for label matching. |
| `--gomplate-path` | `gomplate` | Path to the [gomplate](https://docs.gomplate.ca/) binary. |
| `--log-level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. |

## Manifest directory layout

The directory may contain any number of `*.yaml` or `*.yml` files. Each file is
decoded by its `kind` field:

- **`ConfigSet`** — applied to the local host when its labels match.
- **`ManagedNode`** — if a `ManagedNode` whose `metadata.name` matches the
  local hostname is found, its labels are used for ConfigSet matching. Any
  other `ManagedNode` objects are available to templates via `(ds "data").Nodes`.

Files of any other kind are ignored.

```
manifests/
├── node.yaml             # ManagedNode for this host (optional)
├── shell-server.yaml     # ConfigSet for all machines
├── shell-workstation.yaml
├── neovim.yaml
└── files/
    ├── zshrc.zsh
    ├── neovim/
    │   └── init.lua.tmpl
    └── server.zsh
```

## Label matching

`ConfigSet` labels act as a selector: a ConfigSet is applied only if every
label on the ConfigSet exists on the local node. This is identical to the
cluster-connected behaviour.

If no `ManagedNode` YAML is found in the manifest directory, the node is
synthesised with a single label — `kubernetes.io/hostname: <hostname>` — and
only ConfigSets that match on hostname (or have no labels) will apply.

To match on OS, role, or other attributes, include a `ManagedNode` in the
manifest directory:

```yaml
apiVersion: common.nodemanager/v1
kind: ManagedNode
metadata:
  name: myhost        # must match the hostname
  labels:
    kubernetes.io/hostname: myhost
    kubernetes.io/os: linux
    nodemanager.io/role: workstation
```

Then a ConfigSet targeted at workstations applies automatically:

```yaml
metadata:
  labels:
    nodemanager.io/role: workstation
```

## Source files

The `sourceFile` field on a file declaration lets dotfiles live as real files
in the repository instead of being embedded inline in the YAML:

```yaml
spec:
  files:
    - path: /home/zach/.zshrc
      ensure: file
      owner: zach
      mode: "0644"
      sourceFile: files/zshrc.zsh          # relative to --manifest-dir

    - path: /home/zach/.config/nvim/init.lua
      ensure: file
      owner: zach
      mode: "0644"
      sourceFile: files/neovim/init.lua.tmpl  # .tmpl → rendered as gomplate template
```

Paths are resolved relative to `--manifest-dir`. Absolute paths are used
as-is.

If the `sourceFile` path ends with `.tmpl`, the content is rendered as a
[gomplate](https://docs.gomplate.ca/) template using the same data context as
the `template` field. The `.tmpl` extension is stripped; the `path` field
controls the destination name.

## Template data in offline mode

Templates in offline mode have access to the local node's labels and status,
but not to Kubernetes Secrets or ConfigMaps:

| Field | Available offline? | Notes |
|---|---|---|
| `Node.Labels` | yes | From the `ManagedNode` in the manifest directory, or synthesised. |
| `Node.Status` | yes | From the `ManagedNode` in the manifest directory; empty if none found. |
| `Node.Secrets` | no | `secretRefs` are skipped with a warning. |
| `Node.ConfigMaps` | no | `configMapRefs` are skipped with a warning. |
| `Nodes` | partial | Only the local node is present. |

See the [template data reference](template-data.md) for full field documentation.

## Limitations compared to cluster-connected mode

| Feature | Cluster-connected | Offline |
|---|---|---|
| `secretRefs` / `configMapRefs` in templates | yes | no (warning) |
| Multi-node `Nodes` list in templates | yes | local node only |
| Service restart locking (`lock_group`) | yes | no (restart is immediate) |
| Conflict detection across ConfigSets | yes | no |
| Status written back to `ManagedNode` | yes | no |
| Filebucket backups | yes | yes (if configured) |

## Example: homedir repository

A dedicated `homedir` repository can use offline apply to manage dotfiles
across machines at different configuration tiers:

```
homedir/
├── manifests/
│   ├── nodes/
│   │   ├── vor.yaml         # ManagedNode for workstation
│   │   └── olaf.yaml        # ManagedNode for special host
│   ├── shell-server.yaml    # ConfigSet: role=server
│   ├── shell-workstation.yaml
│   ├── neovim-base.yaml     # ConfigSet: role=special or role=workstation
│   └── neovim-full.yaml     # ConfigSet: role=workstation
└── files/
    ├── zsh/
    │   ├── server.zsh
    │   └── workstation.zsh
    └── neovim/
        └── init.lua
```

`shell-server.yaml`:

```yaml
apiVersion: common.nodemanager/v1
kind: ConfigSet
metadata:
  name: shell-server
  labels:
    nodemanager.io/role: server
spec:
  files:
    - path: /home/zach/.zshrc
      ensure: file
      owner: zach
      mode: "0644"
      sourceFile: files/zsh/server.zsh
```

`shell-workstation.yaml`:

```yaml
apiVersion: common.nodemanager/v1
kind: ConfigSet
metadata:
  name: shell-workstation
  labels:
    nodemanager.io/role: workstation
spec:
  files:
    - path: /home/zach/.zshrc
      ensure: file
      owner: zach
      mode: "0644"
      sourceFile: files/zsh/workstation.zsh
```

Apply on the local workstation:

```sh
nodemanager apply --manifest-dir ./manifests/
```

The same binary and the same manifests work on any machine regardless of
cluster connectivity.
