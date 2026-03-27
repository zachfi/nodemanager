# NodeManagerUpgradeFailed

**Severity:** critical

## Meaning

A scheduled OS upgrade failed on a node. The `node` label identifies which
host is affected.

## Impact

The node is running unpatched software. If the upgrade was part of a group,
the distributed lock may still be held, blocking other nodes in the group from
upgrading.

## Diagnosis

Check the controller logs around the time of the failure:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=2h | grep -i upgrade
```

Check whether the node still holds the upgrade lock:

```sh
kubectl get lease -n nodemanager
```

## Remediation

- **Package manager failure**: SSH to the node and resolve the package manager
  state manually (`pacman -Syu`, `pkg upgrade`, etc.), then delete the
  `AnnotationLastUpgrade` annotation on the ManagedNode to allow a retry.
- **Stuck lock**: if the node holds an upgrade group lease and will not
  release it (e.g. the controller crashed mid-upgrade), delete the lease
  manually:

```sh
kubectl delete lease -n nodemanager <group-name>
```

- **Reboot required**: if the upgrade succeeded but the node did not reboot,
  the controller will retry on the next reconcile.
