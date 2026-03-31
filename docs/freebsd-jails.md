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
7. The jail is started via `service jail start <name>`.

When a `Jail` object is deleted:

1. The jail is stopped.
2. The `jail.conf` fragment and fstab are removed.
3. The jail ZFS datasets are recursively destroyed.
4. The release snapshot created for this jail is removed.

The reconciler only manages jails assigned to its own hostname via the
`nodeName` field — jails for other hosts are ignored.

## Spec

| Field | Type | Description |
|---|---|---|
| `nodeName` | string | **Required.** Hostname of the node that should run this jail. |
| `release` | string | **Required.** FreeBSD release, e.g. `14.2-RELEASE`. |
| `hostname` | string | Jail hostname. Defaults to the object name. |
| `interface` | string | Network interface to bind (e.g. `em0`). |
| `inet` | string | IPv4 address (e.g. `192.168.1.20/24`). |
| `inet6` | string | IPv6 address. |
| `mounts` | list | Nullfs mounts from the host into the jail. |

### mounts

| Field | Type | Description |
|---|---|---|
| `hostPath` | string | Path on the host to mount. |
| `jailPath` | string | Mount point inside the jail. |
| `type` | string | Mount type. Defaults to `nullfs`. |
| `readOnly` | bool | Mount read-only. Defaults to `false`. |

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
  nodeName: fbsd01.example.com
  release: "14.2-RELEASE"
  hostname: web01
  interface: em0
  inet: "192.168.1.20/24"
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
- `service(8)` available (standard on FreeBSD)
