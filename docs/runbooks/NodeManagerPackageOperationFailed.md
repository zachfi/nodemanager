# NodeManagerPackageOperationFailed

**Severity:** warning

## Meaning

A package operation (`install`, `remove`, or `upgrade`) failed on a node. The
`operation` and `node` labels identify what failed; for install/remove the
`package` label scopes it to the offending package.

## Impact

The node's package state does not match the ConfigSet. A required package may be
missing, or an unwanted one may still be installed.

## Diagnosis

Check the controller logs for the underlying package-manager error:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=20m | grep -i -E 'pacman|apk|pkg|package'
```

SSH to the node and reproduce the operation manually to see the real error:

```sh
# Arch:    pacman -S <pkg>
# Alpine:  apk add <pkg>
# FreeBSD: pkg install <pkg>
```

## Remediation

- **Package not found**: the name may be wrong for this OS, or the repo metadata
  is stale — refresh the package database (`pacman -Sy`, `apk update`,
  `pkg update`).
- **Locked database**: a previous run or a manual session may hold the lock;
  clear the stale lock and retry.
- **Disk full / network**: verify free space and that the node can reach its
  package mirrors.
