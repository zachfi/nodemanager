# Deployment

nodemanager is a single statically-linked binary. It runs directly on the host
it manages, connecting to the Kubernetes API server via a kubeconfig or
in-cluster service account.

## Bootstrap

Two subcommands handle getting a new node connected to the cluster.

### `nodemanager token` — generate a short-lived bootstrap credential (run from admin machine)

Creates all Kubernetes resources for the named node (namespace, ClusterRole,
ServiceAccount, ClusterRoleBinding) then issues a **short-lived** kubeconfig.
Hand this to the new host; it expires automatically after the TTL.

```sh
# Output to a file, copy to the new host:
nodemanager token --hostname myhost --out myhost-bootstrap.kubeconfig

# Pipe directly over SSH:
nodemanager token --hostname myhost | ssh myhost \
  "cat > /tmp/bootstrap.kubeconfig && nodemanager bootstrap --bootstrap-kubeconfig /tmp/bootstrap.kubeconfig"
```

| Flag | Default | Description |
|---|---|---|
| `--hostname` | *(required)* | Target node name. |
| `--kubeconfig` | `~/.kube/config` | Admin kubeconfig. |
| `--namespace` | `nodemanager` | Namespace for nodemanager objects. |
| `--ttl` | `1h` | Lifetime of the temporary credential. |
| `--out` | stdout | Write kubeconfig to this file instead of stdout. |

### `nodemanager bootstrap` — exchange the credential for a long-lived kubeconfig (run on the new host)

Uses the bootstrap kubeconfig (short-lived from `token`, or a broad admin
kubeconfig) to write a permanent node-scoped kubeconfig. Creates any missing
Kubernetes resources, then requests a long-lived token the node keeps.

```sh
nodemanager bootstrap --bootstrap-kubeconfig /tmp/bootstrap.kubeconfig

# The node-scoped kubeconfig is written to:
#   Linux:   /etc/nodemanager/kubeconfig
#   FreeBSD: /usr/local/etc/nodemanager/kubeconfig

# Delete the temporary credential:
rm /tmp/bootstrap.kubeconfig
```

| Flag | Default | Description |
|---|---|---|
| `--bootstrap-kubeconfig` | *(required)* | Short-lived or admin kubeconfig. |
| `--kubeconfig` | `/etc/nodemanager/kubeconfig` | Where to write the permanent kubeconfig. |
| `--namespace` | `nodemanager` | Namespace for nodemanager objects. |
| `--hostname` | `os.Hostname()` | Node name (must match the one used in `token`). |
| `--token-ttl` | `8760h` | Lifetime of the issued long-lived token. |

Both commands are idempotent — safe to re-run to rotate credentials.

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
