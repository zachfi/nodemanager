# NodeManagerFileDrift

**Severity:** warning

## Meaning

Files managed by a ConfigSet on a node have been changing continuously for more
than 20 minutes. The `configset` and `node` labels identify the pair. The
rendered content never stabilizes, so the SHA256 hash mismatches every reconcile
and the file is rewritten each time.

## Impact

Every reconcile rewrites the file and restarts any subscribed services. This
causes needless churn — service flaps, log noise, and wasted disk writes (a real
concern on SD-card hosts).

## Diagnosis

Compare the rendered output across two reconciles to spot the instability:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=20m | grep -i -E 'file|drift|hash'
```

The usual cause is non-deterministic template output — most often an unsorted
collection (node list, map iteration) that re-orders between renders.

## Remediation

- **Sort template inputs**: ensure any ranged-over list (nodes, peers, members)
  is explicitly sorted in the template so output is stable.
- **Pin volatile values**: drop or freeze fields that change every render
  (timestamps, random IDs) unless they are genuinely required.
- **Confirm it clears**: after fixing the template, the hash should stabilize and
  `nodemanager_file_changes_total` should stop incrementing for that pair.
