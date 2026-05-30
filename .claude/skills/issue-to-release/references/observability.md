# Observability & notification-discipline rubric

Shared between `/issue-to-release` and `/fleet-sre`. The goal is a system that
tells us when something is actually wrong — and stays quiet otherwise. Alert
fatigue is a reliability problem in its own right: an operator who learns to
ignore alerts misses the one that matters.

## Where observability lives in this repo

| Artifact | Location | Convention |
|---|---|---|
| Metrics | `internal/controller/common/metrics.go`, `internal/controller/freebsd/metrics.go` | Prometheus collectors registered with controller-runtime |
| Alerts | `monitoring/alerts/nodemanager.libsonnet` | jsonnet rule objects; rendered + pushed to Mimir by an external job every 5 min |
| Dashboards | `monitoring/dashboards/` (`nodemanager-configset`, `nodemanager-agent-health`) | jsonnet; rendered via `monitoring/render-dashboards.jsonnet` |
| Runbooks | `docs/runbooks/<AlertName>.md` | One page per alert, 1:1 |

The `runbook_url` annotation is **auto-derived from the alert name** in
`nodemanager.libsonnet` (see the `runbookBase` local), so a new alert gets its
runbook link for free — but the page must exist or the link 404s.

## The "will it resolve on its own?" test

Before proposing or keeping any alert, answer this explicitly. It's the core
question that decides whether something should notify a human.

- **Self-resolving / transient / informational** → don't notify. Either a
  dashboard panel only, or `severity: warning` with a generous `for:` so a blip
  never pages. Examples: a single reconcile error that the next reconcile clears;
  brief latency spikes; expected churn during a rollout.
- **Won't self-resolve AND is actionable by a human** → notify. Examples:
  crash/restart loop, reconcile stuck (no successful apply in N minutes), upgrade
  lease stuck, upgrade failed, ConfigSet apply failing persistently, agent
  version skew past threshold. These sit until someone acts.

The existing rules already encode this with `for:` windows (5m/10m) and stale/
stuck timestamp expressions — match that style. Prefer expressing "stuck" as a
timestamp-age condition (`time() - ..._last_..._timestamp_seconds > N`) rather
than a momentary error, because age conditions are inherently self-clearing and
won't flap.

## Severity tiers

- `severity: warning` — visible on dashboards / non-paging review. The default.
- `severity: critical` — reserve for won't-self-resolve-and-actionable. This is
  what should reach a human out-of-band.

When introducing the first `critical` alert, confirm the routing actually sends a
notification (and only `critical` does) rather than assuming it.

## Alert ⇄ runbook ⇄ dashboard

- **Every new alert needs a `docs/runbooks/<AlertName>.md` page.** Match the
  existing runbook structure (symptom, likely causes, how to confirm, how to
  resolve, related). A missing page means a broken `runbook_url`.
- **Dashboards get a panel when it aids triage** — not every alert needs one. Add
  a panel when seeing the metric over time helps an operator decide what to do.

## Selector correctness (a real, easy-to-miss failure mode)

Alert/dashboard queries must actually match the series in prod. nodemanager runs
both in-cluster (the controller) and off-cluster on hosts (the agent), and the
two are scraped under **different `job` labels** — off-cluster agents land under a
generic local-scrape job, not `job=~"nodemanager.*"`. An alert whose selector
doesn't match any series is silently dead. When adding or auditing a rule, verify
the selector returns series for the instances it's meant to cover (see
`/fleet-sre`, which audits this directly).
