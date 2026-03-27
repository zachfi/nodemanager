# NodeManagerReconcileErrors

**Severity:** warning

## Meaning

A nodemanager controller (`managednode` or `configset`) has been returning
errors from its reconcile loop for more than 5 minutes.

## Impact

The affected controller is not converging. Nodes matched by failing ConfigSets
may have stale package, file, or service state.

## Diagnosis

Check the controller logs for the failing node:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=10m | grep -i error
```

The `controller` label on the alert identifies which reconciler is failing.

## Remediation

- **API errors** (permission denied, not found): verify RBAC and that the CRDs
  are installed.
- **System errors** (package manager failure, file write error): check the node
  directly — disk full, locked package database, etc.
- **Transient errors**: the controller will retry automatically; confirm the
  alert clears within a few minutes.
