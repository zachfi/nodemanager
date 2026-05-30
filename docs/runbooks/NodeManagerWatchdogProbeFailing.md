# NodeManagerWatchdogProbeFailing

## Summary

The watchdog's API connectivity probe on a node has been failing with no
successes for 10 minutes. The agent does a direct, uncached read of its own
ManagedNode each watchdog tick; failure means it cannot reach the Kubernetes
API server. If failure persists past `--watchdog.stale-threshold`, the agent
exits(74) and the supervisor restarts it.

## Symptom

- Alert `NodeManagerWatchdogProbeFailing` firing for node `{{ node }}`.
- `rate(nodemanager_watchdog_probe_total{result="error"}[10m]) > 0` with no
  matching `result="success"`.

## Likely causes

- API server unreachable from the node (network partition, DNS, firewall).
- API server / control plane unhealthy (see deployment_tools#1 — etcd fsync /
  leader churn).
- Expired or invalid kubeconfig / credentials on the agent.
- The node's route to the in-cluster service endpoint changed.

## How to confirm

- Check whether the agent has been restarting: `changes(process_start_time_seconds{instance="<node>"}[1h])`.
- From the node, test API reachability directly (kubeconfig the agent uses).
- Check control-plane health (apiserver, etcd) for the same window.

## How to resolve

- If the control plane is unhealthy, fix that first; the probe recovers on its own.
- If credentials/route are wrong on the node, correct them and let the agent restart.
- If the agent is crash-looping (restart not recovering), treat as an incident —
  see `NodeManagerAgentRestartLoop`.

## Related

- `NodeManagerAgentRestartLoop`
- znet/nodemanager#20 (watchdog connectivity probe)
- znet/deployment_tools#16 (supervisor restart-on-exit-74; reconcile-period sizing)
