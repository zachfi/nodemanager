# PoudriereBulk Command Executor Bridges

Reference bridge scripts implementing the v1 `PoudriereBulk` Command
executor contract for specific upstream build systems.

The contract is documented in
[`docs/poudriere/command-contract.md`](../../../docs/poudriere/command-contract.md).
The threat model and required script-author hygiene are in
[`docs/security/poudriere-bridges.md`](../../../docs/security/poudriere-bridges.md).
Read those before authoring new bridges.

## Available bridges

| Pair | Target | Notes |
|---|---|---|
| `poudriere-dispatch-forgejo.sh` + `poudriere-status-forgejo.sh` | Forgejo Actions `workflow_dispatch` | Handles Forgejo's quirk of not returning the run ID from `/dispatches` by polling `/runs` after a short delay. Translates `status` × `conclusion` into the contract's `state`. |

Each pair is independent. A bridge writer authoring a new target
(Woodpecker, Drone, GitHub Actions, a remote `ssh` invocation, …)
implements both halves of the pair against the contract; nothing else
in nodemanager needs to change.

## Deployment

These scripts are designed to be installed on the host where
nodemanager runs. The clean way is via a `ConfigSet` that delivers
both scripts and references their paths from a `PoudriereBulk`'s
`spec.executor.command.{dispatch,status}` arrays. That keeps the
bridges declarative alongside the rest of the host's configuration.

Example deployment shape:

```yaml
apiVersion: common.nodemanager/v1
kind: ConfigSet
metadata:
  name: poudriere-bridges
  namespace: nodemanager
  labels: { freebsd.nodemanager/poudriere: enabled }
spec:
  files:
    - path: /usr/local/libexec/poudriere-dispatch-forgejo.sh
      mode: "0755"
      content: |
        # ... contents of poudriere-dispatch-forgejo.sh ...
    - path: /usr/local/libexec/poudriere-status-forgejo.sh
      mode: "0755"
      content: |
        # ... contents of poudriere-status-forgejo.sh ...
  packages:
    - name: jq
    - name: curl
```

## Required tools on the build host

Both reference bridges need:

- `/bin/sh` (any POSIX-compliant shell)
- `curl`
- `jq`

`curl` and `jq` are available in the FreeBSD ports tree as
`ftp/curl` and `textproc/jq`. A bridge written in Go would have no
such dependency.

## Migrating from in-process to Command

A `PoudriereBulk` migrates between executors by editing
`spec.executor`:

```diff
 apiVersion: freebsd.nodemanager/v1
 kind: PoudriereBulk
 metadata:
   name: personal-amd64
 spec:
   jail: 14amd64
   tree: personal
   ports: [net/curl, shells/zsh]
+  executor:
+    type: Command
+    command:
+      dispatch: ["/usr/local/libexec/poudriere-dispatch-forgejo.sh"]
+      status:   ["/usr/local/libexec/poudriere-status-forgejo.sh"]
+      env:
+        - { name: FORGEJO_BASE_URL, value: https://code.znet }
+        - { name: FORGEJO_REPO,     value: zachfi/build-infra }
+        - { name: FORGEJO_WORKFLOW, value: poudriere-build.yml }
+      secretEnv:
+        - name: FORGEJO_TOKEN
+          secretRef: { name: forgejo-poudriere-pat, key: token }
```

Removing the `executor` block again reverts to the InProcess executor.
The migration is non-destructive: `status.lastBuildResult` and
`status.lastBuildTime` keep their meaning across the switch.
