# Forgejo Actions workflows for the PoudriereBulk Command executor

This directory holds reference Forgejo Actions workflow definitions
that pair with the bridge scripts in `../scripts/`. Together they
form the third leg of a Command-mode deployment:

```
PoudriereBulk CR
       │
       ▼
nodemanager  ──invoke──▶  bridge dispatch script
                                  │
                            workflow_dispatch
                                  │
                                  ▼
                          Forgejo Actions runner
                                  │
                            poudriere bulk
```

## Files

| File | Pairs with | Purpose |
|---|---|---|
| `poudriere-build.yml` | `../scripts/poudriere-{dispatch,status}-forgejo.sh` | Generic build workflow accepting (jail, tree, ports, bulk) inputs. Runs `poudriere bulk` on a self-hosted FreeBSD runner. Concurrency group serializes per (jail, tree) pair. |

## Where this file goes

In the Forgejo repo your `PoudriereBulk.spec.executor.command.env`
points at via `FORGEJO_REPO`, place the file at
`.forgejo/workflows/poudriere-build.yml`. The filename matches the
`FORGEJO_WORKFLOW` env value the dispatch bridge sends. One repo can
hold multiple build-related workflows; they share the runner pool
but each has its own `workflow_dispatch` endpoint.

## Runner setup (deployment-side)

Out of scope for this repo, but in summary:

1. Install Forgejo Actions runner on a FreeBSD host with poudriere
   already configured (typically the build jail itself, e.g. poud1).
2. Register the runner with labels `[self-hosted, freebsd, poudriere]`.
3. Ensure the runner user can execute `poudriere` and `portshaker`
   (root, or a user with NOPASSWD doas/sudo).
4. Confirm `poudriere jail -l` shows the build jails this workflow
   will reference (provisioned either manually or via
   `PoudriereJail` CRs reconciled by an InProcess executor on the
   same host).

The first time you cut a `PoudriereBulk` over to `executor.type=Command`,
watch its status with `kubectl describe poudrierebulk` while
simultaneously following the runner's logs — `lastDispatchTime` and
`lastDispatchedRunID` should populate within a few seconds of the
dispatch script returning, and the run URL appears once the status
program polls successfully.

## Adding a workflow for a different system

The workflow file is generic — it accepts the contract's inputs and
runs poudriere with them. Targeting a different CI system (Woodpecker,
GitHub Actions, etc.) means writing:

1. A workflow file in that system's syntax that accepts the same
   inputs and runs `poudriere bulk`.
2. Bridge scripts (`dispatch` and `status`) for that system. See
   `../scripts/README.md` and `docs/poudriere/command-contract.md`.

Nothing in nodemanager changes.
