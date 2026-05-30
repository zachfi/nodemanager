# NodeManagerVersionSkew

**Severity:** warning

## Meaning

More than one distinct nodemanager agent version is reporting across the fleet,
as counted from `nodemanager_build_info`. Agents are installed via the node
package / FreeBSD port rather than a single cluster image tag, so they drift
independently and a rollout can stall partway.

## Impact

Nodes on older versions miss bug fixes and behavior changes. Skew makes the
fleet harder to reason about — a bug "fixed" upstream may still be live on the
laggards.

## Diagnosis

Break down the fleet by version:

```promql
count by (version) (nodemanager_build_info)
```

List the lagging nodes so you know what to roll forward:

```promql
nodemanager_build_info
```

> **Coverage caveat:** a node only appears here if its local alloy scrapes the
> nodemanager metrics endpoint (job `prometheus.scrape.local`). Nodes that are
> not scraped are invisible to this alert — broadening scrape coverage is part
> of the same effort and is tracked deployment-side.

## Remediation

- **Roll forward laggards**: bump the node package / FreeBSD port on the
  out-of-date hosts to the current release and confirm `nodemanager_build_info`
  converges to a single `version`.
- **Persistent single laggard**: a node stuck many minor versions back (e.g. an
  intermittently-powered laptop) may simply need to be online long enough to
  update — confirm it is reachable and the package source is current.
- **Expected transient skew**: during a deliberate staged rollout this alert may
  fire briefly; it should clear once the rollout completes.
