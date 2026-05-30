# NodeManagerConfigSetApplyStale

**Severity:** warning

## Meaning

A ConfigSet has not been successfully applied on a node in over 30 minutes. The
`configset` and `node` labels identify the affected pair. Unlike
`NodeManagerConfigSetApplyError`, this fires on *silence* — the apply is not
erroring, it simply is not happening.

## Impact

The node may be drifting from desired state without any visible error. A change
pushed to the ConfigSet will not take effect until reconciles resume.

## Diagnosis

Confirm whether the controller is reconciling this node at all:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=30m | grep -i configset
```

Check when the apply last succeeded and whether the agent is even connected:

```promql
time() - nodemanager_last_configset_apply_timestamp_seconds
```

```sh
kubectl get managednode -n nodemanager <node> -o jsonpath='{.status}' | jq .
```

## Remediation

- **Agent down / not scraped**: if the node stopped reporting entirely, treat it
  as an agent-health problem — see `NodeManagerAgentRestartLoop` and
  `NodeManagerReconcileStuck`.
- **Selector mismatch**: a label change on the node or ConfigSet may have
  de-matched them. Verify the ConfigSet's `nodeSelector` still matches the
  ManagedNode's labels.
- **Reconcile wedged**: if other ConfigSets on the same node are also stale, the
  controller may be stuck — capture a goroutine dump before restarting.
