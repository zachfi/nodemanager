# FreeBSD Jails

nodemanager can provision and manage FreeBSD jails using ZFS datasets, treating
them as first-class resources via the `Jail` CRD.

**API group:** `freebsd.nodemanager` / **version:** `v1`

## How it works

Jails are backed by ZFS CoW clones of a downloaded FreeBSD release. This
gives each jail its own independent root filesystem while sharing storage with
the release until the jail diverges — making creation nearly instantaneous and
space-efficient.

Dataset layout (under the configured base dataset, e.g. `zroot/nodemanager`):

```
zroot/nodemanager/releases/14.2-RELEASE     ← extracted base system
zroot/nodemanager/jails/web01               ← per-jail container dataset
zroot/nodemanager/jails/web01/root          ← ZFS clone of the release snapshot
```

## Lifecycle

When a `Jail` object is created:

1. The FreeBSD release is downloaded from the official mirror and extracted
   (idempotent — skipped if already present).
2. A ZFS snapshot of the release dataset is taken.
3. The jail root is cloned from the snapshot.
4. `/etc/resolv.conf` and `/etc/localtime` are seeded into the jail root.
5. A `jail.conf` fragment is written to `/etc/jail.conf.d/<name>.conf`.
6. An fstab is written if `mounts` are declared.
7. The jail is started via `jail -c`.
8. If the jail references a JailTemplate with `postCreate` hooks, those
   commands are executed inside the jail via `jexec` on first start.

When a `Jail` object is deleted:

1. The jail is stopped via `jail -r`.
2. The `jail.conf` fragment and fstab are removed.
3. The jail ZFS datasets are recursively destroyed.
4. The release snapshot created for this jail is removed.

The reconciler only manages jails assigned to its own hostname via the
`nodeName` field — jails for other hosts are ignored.

## JailTemplate

A `JailTemplate` provides shared defaults that multiple Jails can inherit.
Jails reference a template via `spec.templateRef`. Jail-level fields always
take precedence over template defaults.

Template-owned fields (shared defaults): `interface`, `mounts`, `update`, `postCreate`.
Jail-owned fields (per-jail identity): `nodeName`, `release`, `hostname`, `inet`, `inet6`.

```yaml
apiVersion: freebsd.nodemanager/v1
kind: JailTemplate
metadata:
  name: standard
  namespace: nodemanager
spec:
  interface: lo1
  update:
    schedule: "0 3 * * *"
    delay: "24h"
    group: jail-updates
  postCreate:
    - name: bootstrap
      command: /usr/local/bin/nodemanager-agent
      args: ["--runonce"]
```

## Jail spec

| Field | Type | Description |
|---|---|---|
| `templateRef` | string | Name of a JailTemplate in the same namespace. |
| `nodeName` | string | **Required.** Hostname of the node that should run this jail. |
| `release` | string | **Required.** FreeBSD release, e.g. `14.2-RELEASE`. |
| `hostname` | string | Jail hostname. Defaults to the object name. |
| `interface` | string | Network interface to bind (e.g. `lo1`). |
| `inet` | string | IPv4 address (e.g. `172.16.20.112/27`). |
| `inet6` | string | IPv6 address (e.g. `2001:db8::5/120`). |
| `mounts` | list | Nullfs mounts from the host into the jail. |
| `update` | object | Periodic freebsd-update schedule. |

### mounts

| Field | Type | Description |
|---|---|---|
| `hostPath` | string | Path on the host to mount. |
| `jailPath` | string | Mount point inside the jail. |
| `type` | string | Mount type. Defaults to `nullfs`. |
| `readOnly` | bool | Mount read-only. Defaults to `false`. |

### update

| Field | Type | Description |
|---|---|---|
| `schedule` | string | Cron expression for when updates should run. |
| `delay` | string | Minimum time between updates (e.g. `24h`). |
| `group` | string | Lease group name for coordinated updates. |

## Status conditions

| Condition | Description |
|---|---|
| `Progressing=True` | Jail is being provisioned or updated. |
| `Progressing=False` | Provisioning complete. |
| `Available=True` | Jail is provisioned and running. |
| `Available=False` | Jail is provisioned but not running after start attempt. |
| `Degraded=True` | An error occurred during provisioning, start, or deletion. |

## Example

```yaml
apiVersion: freebsd.nodemanager/v1
kind: Jail
metadata:
  name: web01
  namespace: nodemanager
spec:
  templateRef: standard
  nodeName: fbsd01.example.com
  release: "14.2-RELEASE"
  inet: "172.16.20.112/27"
  inet6: "2001:470:e8af:20::532/120"
  mounts:
    - hostPath: /data/www
      jailPath: /var/www
      readOnly: true
```

## Controller configuration

The jail controller requires two configuration values set in the nodemanager
config:

```yaml
freebsd:
  jail:
    jailDataPath: /usr/local/nodemanager   # base filesystem path
    zfsDataset: zroot/nodemanager          # base ZFS dataset
```

The controller will ensure the dataset hierarchy exists on startup.

## Prerequisites

- ZFS available on the host
- `bsdtar` installed (for release extraction)
