# NodeManagerJailOperationFailed

**Severity:** warning

## Meaning

A FreeBSD jail operation (create, start, stop, provision, etc.) failed on a
node. The `operation`, `jail`, and `node` labels identify what failed where.

## Impact

The jail is not in its desired state. Depending on the operation this may mean a
jail did not start, did not provision, or is stuck in a partial configuration.

## Diagnosis

Check the controller logs and the jail's status conditions:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=30m | grep -i jail
kubectl describe jail -n nodemanager <jail>
```

On the build/host node, inspect the live jail state:

```sh
jls
jail -e | grep <jail>
```

## Remediation

- **Networking**: jail networking uses loopback + BGP with per-node v4/v6 subnet
  pools and manual IPAM — a failed start is often an address conflict or a
  missing route. Verify the assigned addresses are free.
- **Provision failure**: check that the rc.d/config templates render and that
  required packages are reachable from inside the jail.
- **Stale state after redeploy**: existing jails may need a reconcile trigger
  after a controller upgrade; confirm the JailReconciler is running and the
  reconcilePeriod is set.
