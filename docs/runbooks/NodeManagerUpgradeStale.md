# NodeManagerUpgradeStale

**Severity:** warning

## Meaning

A node has not recorded a successful OS upgrade in 7 days. The `node` label
identifies the host. This is driven by
`nodemanager_last_upgrade_timestamp_seconds`, so a node that has *never* upgraded
will not fire this alert until it has recorded at least one upgrade — for
fleet-wide "no upgrades at all", see `NodeManagerVersionSkew` and the scrape
coverage gap.

## Impact

The node is running progressively more out-of-date software. Security patches
and dependency fixes are not being applied.

## Diagnosis

Confirm the node's upgrade schedule and whether reconciles are running:

```sh
kubectl get managednode -n nodemanager <node> -o jsonpath='{.spec.upgrade}' | jq .
```

Check whether upgrades are being requested but not completing — a stuck
approval or a held group lock both prevent the timestamp from advancing:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=24h | grep -i -E 'upgrade|approval|lock'
kubectl get lease -n nodemanager
```

## Remediation

- **No schedule set**: if `spec.upgrade.schedule` is empty the node only upgrades
  on a force annotation. Add a cron schedule if periodic upgrades are intended.
- **Approval never resolves**: confirm the desktop agent is connected and
  responding; a dismissed prompt now maps to DELAY and retries next cycle.
- **Group lock starvation**: if many nodes share one `upgrade.group`, only one
  upgrades per slot — verify the group is draining and no node holds a stale
  lease.
