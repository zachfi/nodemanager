# ConfigSet

`ConfigSet` declares the desired state for a set of nodes — packages, files,
services, and executions. It is matched to nodes via label selectors: the
controller only applies a `ConfigSet` if all of its labels match the labels on
the local `ManagedNode`.

**API group:** `common.nodemanager` / **version:** `v1`

## Spec

### packages

| Field | Type | Description |
|---|---|---|
| `name` | string | Package name. |
| `ensure` | string | `installed` or `absent`. |

### files

| Field | Type | Description |
|---|---|---|
| `path` | string | Absolute path on disk. |
| `ensure` | string | `file`, `directory`, `symlink`, or `absent`. |
| `content` | string | Literal file content. |
| `template` | string | [gomplate](https://docs.gomplate.ca/) template string. |
| `owner` | string | File owner (username). |
| `group` | string | File group. |
| `mode` | string | File permissions (e.g. `0644`). |
| `target` | string | Symlink target (when `ensure: symlink`). |
| `secretRefs` | list | Kubernetes Secret names whose data is available in templates. |
| `configMapRefs` | list | Kubernetes ConfigMap names whose data is available in templates. |

### services

| Field | Type | Description |
|---|---|---|
| `name` | string | Service unit name. |
| `ensure` | string | `running` or `stopped`. |
| `enable` | bool | Whether the service should be enabled at boot. |
| `arguments` | string | Override service arguments (rc.d). |
| `user` | string | Run as a systemd user service for this user. |
| `subscribe_files` | list | Restart the service when any listed file path changes. |
| `lock_group` | string | Lease group — only one service in the group restarts at a time. |

### executions

| Field | Type | Description |
|---|---|---|
| `command` | string | Command to run. |
| `args` | list | Arguments. |
| `subscribe_files` | list | Run the command when any listed file path changes. |

## Example

```yaml
apiVersion: common.nodemanager/v1
kind: ConfigSet
metadata:
  labels:
    kubernetes.io/os: arch
  name: clock-linux
  namespace: nodemanager
spec:
  packages:
    - ensure: installed
      name: chrony
  files:
    - path: /etc/chrony.conf
      ensure: file
      owner: root
      group: root
      mode: "0644"
      content: |
        pool 2.arch.pool.ntp.org iburst
        rtcsync
  services:
    - name: chronyd
      ensure: running
      enable: true
      subscribe_files:
        - /etc/chrony.conf
    - name: systemd-timesyncd
      ensure: stopped
      enable: false
```
