# NodeManagerPoudriereBuildLong

**Severity:** warning

## Meaning

The 95th-percentile run duration for a Poudriere bulk has exceeded 4 hours (the
histogram's top bucket) over the last hour. The `bulk` and `node` labels
identify it. Unlike `NodeManagerPoudriereBuildStale`, runs are still completing —
they just take far too long.

## Impact

Long-running builds block sibling bulks behind the workqueue rate limiter and
delay package availability. A genuinely stuck build can monopolize the build
host.

## Diagnosis

See whether one port is hanging or the dependency graph blew up:

```sh
# On the build node:
poudriere status -j <bulk>
poudriere bulk -j <bulk> -n   # dry-run: shows what would be built
```

Check the duration trend:

```promql
histogram_quantile(0.95,
  sum by (node, bulk, le) (rate(nodemanager_poudriere_bulk_duration_seconds_bucket[1h]))
)
```

## Remediation

- **Single hanging port**: identify the port stuck in `build` and investigate it
  directly (infinite configure loop, network fetch stall).
- **Circular / exploded dependencies**: a newly introduced dependency cycle or a
  large tree expansion can balloon build time — review recent ports changes.
- **Host contention**: confirm the build node has adequate CPU/IO and is not
  swapping; consider raising parallelism or splitting the bulk.
