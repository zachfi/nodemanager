# NodeManagerConfigSetApplyError

**Severity:** warning

## Meaning

A specific ConfigSet failed to apply on a node. The `configset` and `node`
labels on the alert identify which pair is failing.

## Impact

The node is not in the desired state described by the ConfigSet. Depending on
the ConfigSet, this may mean a package is not installed, a config file is
outdated, or a service is not running.

## Diagnosis

Check the ManagedNode status for the error detail:

```sh
kubectl get managednode -n nodemanager <node> -o jsonpath='{.status.configsets}' | jq .
```

The `error` field on the matching ConfigSet entry will contain the failure
message.

## Remediation

- **Package errors**: the package may not exist in the repository, or the
  package manager is in a broken state. SSH to the node and verify manually.
- **File errors**: check permissions on the target path, or whether a
  referenced Secret/ConfigMap exists in the namespace.
- **Template errors**: verify `gomplate` is installed on the node and that
  the template syntax is valid.
- **Service errors**: check `systemctl status <service>` or equivalent on the
  node.
