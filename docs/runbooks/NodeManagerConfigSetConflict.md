# NodeManagerConfigSetConflict

**Severity:** warning

## Meaning

Two ConfigSets claim the same file path or service name on the same node. The
`configset` and `node` labels identify the ConfigSet that lost the conflict — it
is **not applied** until the overlap is resolved.

## Impact

The conflicting ConfigSet's files/services are not being managed on the node, so
that node is partially out of desired state. The other ConfigSet continues to
apply normally.

## Diagnosis

Inspect the conflict detail recorded on the ManagedNode status:

```sh
kubectl get managednode -n nodemanager <node> \
  -o jsonpath='{.status.configsets[*].conflicts}' | jq .
```

Find the other ConfigSet declaring the same resource:

```sh
kubectl get configset -n nodemanager -o yaml | \
  grep -nE 'name:|path:|files:|services:'
```

## Remediation

- **Remove the duplicate**: decide which ConfigSet should own the file or
  service and delete that resource from the other one.
- **Split selectors**: if two ConfigSets legitimately target overlapping nodes
  with different intents, narrow their `nodeSelector`s so only one matches each
  node.
- Once the overlap is gone the previously-blocked ConfigSet applies on the next
  reconcile and the alert clears.
