---
name: fleet-sre
description: >-
  Play SRE over the nodemanager fleet's telemetry (Mimir metrics, Loki logs,
  Tempo traces via gcx) to find problems worth fixing, then draft Forgejo issues
  for them — proposing before filing. Use this whenever the user wants to "check
  the fleet", "look for problems", "SRE sweep", asks if nodemanager is healthy,
  wants to know whether anything needs fixing in prod, mentions reliability /
  resource use / k8s API load concerns, or wants telemetry turned into tracked
  work. Optimizes for a stable, predictable, low-resource system with very low
  Kubernetes API load. Files code fixes on znet/nodemanager and config/deploy
  issues on znet/deployment_tools (code.znet). Trigger it even for a casual
  "anything broken out there?".
---

# fleet-sre

Read the fleet's signals, judge them like an SRE whose mandate is *a stable,
predictable, low-resource system with very low Kubernetes API load*, and turn real
problems into well-evidenced issues. Nothing gets filed without the user's
approval — the output of an analysis pass is a findings report plus draft issues,
not a flurry of new tickets.

All collection is **read-only via `gcx`** and adds no load to the Kubernetes API —
that matters, because reducing API load is one of the goals, so the diagnostic
must not itself be a source of it.

## Preflight

```sh
fj -H code.znet whoami           # filing target reachable
gcx datasources list             # telemetry reachable
git -C . rev-parse --short HEAD  # which agent version is current in source
```

## Telemetry quick reference

Datasources (query by name; UID in parens as fallback):

| Signal | Datasource | Notes |
|---|---|---|
| Metrics | `Mimir` (`PAE45454D0EDB9216`) | PromQL |
| Logs | `Loki` (`P8E80F9AEF21F6940`) | LogQL |
| Traces | `TempoOps` (`P83373D1587495477`) | TraceQL; `service.name = "nodemanager"` |

**Label reality (don't assume a `job=nodemanager.*` selector):** nodemanager runs
in-cluster (the controller) *and* off-cluster on hosts (the agent). Series carry
`instance` / `hostname` / `node` / `os` labels, but the **`job` label differs by
scrape source** — off-cluster agents land under a generic local-scrape job
(e.g. `prometheus.scrape.local`), not `job=~"nodemanager.*"`. Select by metric
name (`{__name__=~"nodemanager_.*"}`) or by `instance`/`hostname`, and treat any
`job=~"nodemanager.*"` selector you find in alerts as suspect until proven to
match series.

Metric families that exist (probe live; don't trust this list as complete):
`nodemanager_configset_apply_total` / `_duration_seconds` / `_last_..._timestamp`,
`nodemanager_configset_applied_resource_version`, `nodemanager_file_changes_total`,
`nodemanager_service_operations_total`, `nodemanager_package_operations_total`,
`nodemanager_upgrade_total` / `_duration_seconds` / `_last_..._timestamp`,
`nodemanager_jail_operations_total`, `nodemanager_poudriere_*`,
`nodemanager_build_info{version=...}` (per-instance version → skew detection), plus
controller-runtime (`controller_runtime_reconcile_*`, `workqueue_*`) and client-go
(`rest_client_requests_total`) **where scraped**.

## 1 — Collect & analyze against the four goals

Query each signal and judge it against the mandate. Always capture the **actual
query and the numbers** — they become the evidence in any issue you draft.

### k8s API load (the headline goal — keep it very low)

```sh
# request rate to the API server from the controller, by verb/resource
gcx metrics query --datasource Mimir 'sum by (verb,host) (rate(rest_client_requests_total[5m]))'
# reconcile throughput — are controllers spinning more than the work justifies?
gcx metrics query --datasource Mimir 'sum by (controller) (rate(controller_runtime_reconcile_total[5m]))'
gcx metrics query --datasource Mimir 'sum by (controller) (rate(workqueue_adds_total[5m]))'
```

Look for: List/Watch churn, resync storms, hot reconcile loops (high reconcile
rate with no corresponding desired-state change), an item re-queued endlessly.
**If `rest_client_requests_total` / `controller_runtime_*` return no series, that
is itself a finding** — the API-load signal isn't observable, which is a scrape
gap, not a clean bill of health.

### Reliability

```sh
gcx metrics query --datasource Mimir 'sum by (result) (increase(nodemanager_configset_apply_total[1h]))'
gcx metrics query --datasource Mimir 'sum by (result,node) (increase(nodemanager_upgrade_total[24h]))'
gcx metrics query --datasource Mimir '(time() - nodemanager_last_configset_apply_timestamp_seconds) > 1800'
gcx logs query --datasource Loki '{instance=~".+"} |~ "(?i)error|panic|failed" |~ "nodemanager"' --since 1h --limit 100
gcx traces query --datasource TempoOps '{ resource.service.name = "nodemanager" && status = error }' --since 1h
```

Look for: persistent apply/upgrade failures, stuck/stale applies, panics, error
spans, retry storms.

### Stability / predictability

```sh
gcx metrics query --datasource Mimir 'count by (instance,version) (nodemanager_build_info)'   # version skew
gcx metrics query --datasource Mimir 'changes(process_start_time_seconds{__name__=~".*"}[6h])' # restarts
gcx traces query --datasource TempoOps '{ resource.service.name = "nodemanager" && duration > 500ms }' --since 1h
```

Look for: inconsistent agent versions across the fleet (and inconsistent version
*formatting*, e.g. `0.18.1` vs `v0.15.1`), restart/crash loops, slow reconciles.

### Resource use

```sh
gcx metrics query --datasource Mimir 'go_memstats_heap_inuse_bytes{__name__=~".*"}'
gcx metrics query --datasource Mimir 'rate(process_cpu_seconds_total[10m])'
gcx metrics query --datasource Mimir 'go_goroutines'
```

Look for: memory growth/leaks over time, CPU out of proportion to the work,
goroutine leaks. Remember the agent runs on Raspberry Pi SD cards and small hosts
— modest footprints matter.

## 2 — Observability-coverage audit

Beyond "is something broken", check **"would we even know if it broke?"** Read
`monitoring/alerts/nodemanager.libsonnet` and for each finding (and each major
failure mode) ask:

- Is there an alert that would catch this class of problem?
- **Does that alert's selector actually match series in prod?** (Given the
  job-label reality above, an alert selecting `job=~"nodemanager.*"` may match
  nothing — a silently dead alert. Verify by running the alert's `expr` through
  `gcx metrics query`.)
- Is an existing alert noisy — fires but self-resolves? That's a finding: tune
  `for:`/threshold or downgrade severity.
- Is there a runbook (`docs/runbooks/`) and, where it aids triage, a dashboard
  panel (`monitoring/dashboards/`)?

The notification philosophy — when something should page vs. stay on a dashboard —
lives in `references/observability.md`. Apply it: we want to know about problems
that won't resolve on their own, and we want silence otherwise.

## 3 — Findings report

Present findings before drafting anything. For each:

```
### <short title>
- Signal:    <metric/log/trace> — <the exact gcx query>
- Evidence:  <the numbers / log lines / trace IDs>
- Severity:  <info | warning | critical> + will-it-self-resolve reasoning
- Hypothesis:<what's likely causing it>
- Fix:       <code change | alert/dashboard/runbook change | config/deploy change>
- Repo:      <znet/nodemanager | znet/deployment_tools> + one-line why
```

### Routing heuristic (you decide, user confirms)

- **`znet/nodemanager`** — the fix is in nodemanager *source*: controller/agent
  behavior, reconcile logic, a metric, an alert/dashboard/runbook (those live in
  this repo), version handling.
- **`znet/deployment_tools`** — the fix is *operational*: scrape config / relabeling,
  manifests, RBAC, resource limits, rollout/version pinning, datasource/Alertmanager
  routing, anything in how the fleet is deployed rather than how the code behaves.

When a finding has both a code and a deploy half (common — e.g. a metric exists in
code but isn't scraped), say so and propose the primary repo, noting the
counterpart.

## 4 — Dedup & draft 🚦 GATE

Before drafting, pull existing open issues on **both** repos so you don't
re-file something already tracked:

```sh
fj -H code.znet issue search -r znet/nodemanager      -s open
fj -H code.znet issue search -r znet/deployment_tools -s open
```

(e.g. issue #13 on znet/nodemanager already owns version-skew + scrape-coverage —
fold new evidence into a comment there rather than opening a duplicate.)

Draft each new issue (title, body with the query + evidence + proposed fix +
repro, suggested labels). **Present the drafts and file only the ones the user
approves.** `fj` flag gotchas (verified): the **title is positional**, the body
is `--body`/`--body-file` (not `--description`), and `--no-template` is needed
since the repos disable blank issues. Pipe the body via stdin to avoid temp files:

```sh
# create an issue (title positional; body on stdin)
fj -H code.znet issue create -r <owner>/<repo> --no-template --body-file /dev/stdin "<title>" <<'EOF'
<markdown body: summary, evidence + exact gcx query, hypothesis, proposed fix>
EOF

# add evidence to an existing issue instead of duplicating (note the owner/repo#N
# form — `issue comment` has no -r flag, it resolves the repo from the spec)
fj -H code.znet issue comment "<owner>/<repo>#<n>" --body-file /dev/stdin <<'EOF'
<markdown comment>
EOF
```

Report back what was filed (with issue numbers) and what was deliberately skipped.

## Reference

- `../issue-to-release/references/observability.md` — notification-discipline
  rubric and where alerts/dashboards/runbooks live. (Same repo; read it for the
  "will it resolve on its own?" test and severity tiers.)
