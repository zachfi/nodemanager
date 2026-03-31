# Deployment

nodemanager is a single statically-linked binary. It runs directly on the host
it manages, connecting to the Kubernetes API server via a kubeconfig or
in-cluster service account.

## Bootstrap

The `bootstrap` subcommand automates the Kubernetes-side setup for a new node.
It uses a broad kubeconfig (e.g. copied from another host or a cluster admin) to
create a node-scoped `ServiceAccount`, `ClusterRoleBinding`, and token, then
writes a minimal kubeconfig that grants only the permissions the node needs.

**On the new host** (after installing the binary):

```sh
# Copy an admin kubeconfig to the host, then:
nodemanager bootstrap --bootstrap-kubeconfig /tmp/admin.kubeconfig

# By default the node-scoped kubeconfig is written to:
#   Linux:   /etc/nodemanager/kubeconfig
#   FreeBSD: /usr/local/etc/nodemanager/kubeconfig

# Delete the broad credentials once bootstrap completes:
rm /tmp/admin.kubeconfig
```

**Options:**

| Flag | Default | Description |
|---|---|---|
| `--bootstrap-kubeconfig` | *(required)* | Kubeconfig with rights to create RBAC and ServiceAccounts. |
| `--kubeconfig` | `/etc/nodemanager/kubeconfig` | Where to write the node-scoped kubeconfig. |
| `--namespace` | `nodemanager` | Namespace for nodemanager objects. |
| `--hostname` | `os.Hostname()` | Node name used to name the ServiceAccount. |
| `--token-ttl` | `8760h` (1 year) | Lifetime of the issued token. Re-run `bootstrap` to rotate. |

Bootstrap is idempotent — safe to re-run on an existing node to rotate the token.

## Binary

Download the latest release binary for your platform from the
[releases page](https://github.com/zachfi/nodemanager/releases) or build from
source:

```sh
make build
# produces bin/manager
```

## Running as a systemd service (Linux)

Create a systemd unit file at `/etc/systemd/system/nodemanager.service`:

```ini
[Unit]
Description=nodemanager
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/nodemanager --kubeconfig /etc/nodemanager/kubeconfig
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=multi-user.target
```

Enable and start:

```sh
systemctl enable --now nodemanager
```

## Running as an rc.d service (FreeBSD)

Create `/usr/local/etc/rc.d/nodemanager`:

```sh
#!/bin/sh
# PROVIDE: nodemanager
# REQUIRE: NETWORKING
# KEYWORD: shutdown

. /etc/rc.subr

name="nodemanager"
rcvar="nodemanager_enable"
command="/usr/local/bin/nodemanager"
command_args="--kubeconfig /usr/local/etc/nodemanager/kubeconfig"
pidfile="/var/run/nodemanager.pid"

load_rc_config $name
run_rc_command "$1"
```

```sh
chmod +x /usr/local/etc/rc.d/nodemanager
sysrc nodemanager_enable=YES
service nodemanager start
```

## Kubeconfig

`nodemanager bootstrap` handles kubeconfig creation automatically. For manual
setup, the required `ClusterRole` is defined in
[`config/rbac/node-role.yaml`](https://github.com/zachfi/nodemanager/blob/main/config/rbac/node-role.yaml).

## Namespace

By default nodemanager operates in the `nodemanager` namespace. Create it
before installing CRDs:

```sh
kubectl create namespace nodemanager
make install
```

## Configuration flags

| Flag | Default | Description |
|---|---|---|
| `--kubeconfig` | `~/.kube/config` | Path to kubeconfig file. |
| `--namespace` | `nodemanager` | Namespace to watch for `ConfigSet` and `ManagedNode` objects. |
| `--leader-elect` | `false` | Enable leader election (only needed when running multiple replicas). |
| `--metrics-bind-address` | `:8080` | Prometheus metrics endpoint. |
| `--health-probe-bind-address` | `:8081` | Health probe endpoint. |
