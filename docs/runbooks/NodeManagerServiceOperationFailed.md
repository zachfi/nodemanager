# NodeManagerServiceOperationFailed

**Severity:** warning

## Meaning

A service operation (`start`, `stop`, `restart`, `enable`, `disable`, `reload`)
failed on a node. The `operation` and `node` labels identify what failed.

## Impact

A managed service is not in its desired run state. Depending on the operation, a
service may be down, not enabled at boot, or running stale configuration because
a reload did not take.

## Diagnosis

Check the controller logs and the service manager directly:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=20m | grep -i service
```

On the node:

```sh
# systemd (Arch/Alpine): systemctl status <service>; journalctl -u <service> -n 50
# FreeBSD rc.d:          service <service> status
```

## Remediation

- **Unit/script missing**: the service name may not exist on this OS, or the
  package providing it failed to install — see
  `NodeManagerPackageOperationFailed`.
- **Bad config**: a service that fails to start right after a config write points
  at a broken rendered file — see `NodeManagerServiceStartLoop` and
  `NodeManagerFileDrift`.
- **Boot enablement**: for `enable`/`disable` failures, verify the init system is
  healthy and the unit is not masked.
