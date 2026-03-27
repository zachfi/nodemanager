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

| Field | Type | Description |
|---|---|---|
| `release` | string | OS release string reported by the node. |
| `interfaces` | map | Non-loopback network interfaces and their IPv4/IPv6 addresses. |
| `configsets` | list | Per-ConfigSet apply results — name, last applied time, and any error. |

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
