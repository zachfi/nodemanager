# NodeManagerPoudriereBuildStale

**Severity:** warning

## Meaning

A Poudriere bulk has not produced any run — success, failure, or skip — in 24
hours. The `bulk` and `node` labels identify it. Most production bulks have
`spec.reconcilePeriod` ≤ 6h, so 24h of silence means runs stopped happening
entirely.

## Impact

Packages for that bulk are no longer being refreshed. Unlike
`NodeManagerPoudriereBuildFailed`, nothing is erroring — the pipeline has simply
gone quiet, which is easy to miss.

## Diagnosis

Check the bulk's status conditions and last-run timestamp:

```sh
kubectl describe poudrierebulk -n nodemanager <bulk>
```

```promql
time() - nodemanager_poudriere_last_bulk_timestamp_seconds
```

Confirm the controller is reconciling and the build host is reachable:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=24h | grep -i -E 'poudriere|bulk'
```

## Remediation

- **Controller wedged**: if other bulks on the same node are also silent, the
  reconcile may be stuck — see `NodeManagerReconcileStuck`.
- **Build host offline**: verify the FreeBSD build node and its Poudriere jail
  are up.
- **Bridge dropping dispatches**: for Command-mode bulks, the dispatch bridge can
  fail silently — check the Forgejo/Woodpecker runner and the dispatch program
  logs. Tune the 24h threshold against your longest expected `reconcilePeriod`.
