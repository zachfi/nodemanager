# Deployment

nodemanager is a single statically-linked binary. It runs directly on the host
it manages, connecting to the Kubernetes API server via a kubeconfig or
in-cluster service account.

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

nodemanager needs read access to `ConfigSet` objects and full access to
`ManagedNode` objects in its namespace. A minimal kubeconfig for an
off-cluster node should reference a `ServiceAccount` token with a bound
`ClusterRole`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nodemanager
rules:
  - apiGroups: ["common.nodemanager"]
    resources: ["configsets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["common.nodemanager"]
    resources: ["managednodes", "managednodes/status"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]
```

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
