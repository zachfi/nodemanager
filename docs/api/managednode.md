# ManagedNode

`ManagedNode` represents a host managed by nodemanager. The controller creates
and owns the `ManagedNode` object for the local hostname — one instance per
running controller.

**API group:** `common.nodemanager` / **version:** `v1`

## Spec

| Field | Type | Description |
|---|---|---|
| `domain` | string | Optional DNS domain for the node. |
| `upgrade.schedule` | string | Cron expression for when upgrades should run. |
| `upgrade.delay` | string | Minimum time between upgrades (e.g. `24h`). Prevents re-upgrading too soon. |
| `upgrade.group` | string | Lease group name. Only one node in the group upgrades at a time. |

## Status

The controller publishes observed host state to the `ManagedNode` status on
every reconcile. This data is available to [ConfigSet templates](../template-data.md)
via `(ds "data").Node.Status` (local node) and `(ds "data").Nodes` (all nodes).

| Field | Type | Description |
|---|---|---|
| `release` | string | OS release string reported by the node. |
| `interfaces` | map | Non-loopback network interfaces and their IPv4/IPv6 addresses. |
| `sshHostKeys` | list | SSH host key fingerprints as SSHFP records (RFC 4255). Present when `ssh-keygen` is available. |
| `wireGuard` | list | WireGuard interface public keys and listen ports. Present when `wg` is installed and interfaces exist. |
| `configsets` | list | Per-ConfigSet apply results — name, last applied time, and any error. |

### interfaces

Each key is the interface name (e.g. `eth0`, `em0`):

| Field | Type | Description |
|---|---|---|
| `ipv4` | list | Non-loopback IPv4 addresses. |
| `ipv6` | list | Global unicast IPv6 addresses. |

### sshHostKeys

Each entry is one SSHFP DNS record:

| Field | Type | Description |
|---|---|---|
| `algorithm` | int | `1`=RSA, `2`=DSA, `3`=ECDSA, `4`=Ed25519. |
| `fingerprintType` | int | `1`=SHA-1, `2`=SHA-256. |
| `fingerprint` | string | Hex-encoded fingerprint. |

### wireGuard

| Field | Type | Description |
|---|---|---|
| `name` | string | Interface name (e.g. `wg0`). |
| `publicKey` | string | Base64-encoded Curve25519 public key. |
| `listenPort` | int | UDP listen port, if configured. |

### configsets

| Field | Type | Description |
|---|---|---|
| `name` | string | ConfigSet name. |
| `resourceVersion` | string | Last reconciled resource version. |
| `lastApplied` | timestamp | Time of last successful apply. |
| `error` | string | Error message from last apply attempt, if any. |

## Example

```yaml
apiVersion: common.nodemanager/v1
kind: ManagedNode
metadata:
  name: myhost
  namespace: nodemanager
spec:
  upgrade:
    schedule: "0 3 * * *"
    delay: 23h
    group: workers
```

After the controller starts, the status will be populated automatically:

```yaml
status:
  release: "rolling"
  interfaces:
    eth0:
      ipv4: ["192.168.1.10"]
      ipv6: ["2001:db8::1"]
  sshHostKeys:
    - algorithm: 3
      fingerprintType: 2
      fingerprint: "5a8be586a92131b2600bd18ee28cf701..."
    - algorithm: 4
      fingerprintType: 2
      fingerprint: "a34ae2f3c47f2baff735acffe4a9244b..."
  wireGuard:
    - name: wg0
      publicKey: "abc123...=="
      listenPort: 51820
```
