# Forgejo Push Trigger

`cmd/forgejo-trigger` is a small HTTP service that receives Forgejo
push webhooks and patches the `freebsd.nodemanager/trigger`
annotation on matching `PoudriereBulk` CRs. The annotation value is
already part of the Bulk's input hash (see
[Command Executor Contract](command-contract.md)), so a successful
patch invalidates the skip-if-unchanged check on the next reconcile
and a fresh build dispatches.

This closes the loop for the case where the **ports tree lives in a
separate Forgejo repo from the build workflow**:

```
git push to ports tree
       │
       ▼
Forgejo webhook (HMAC-signed POST to /webhook)
       │
       ▼
forgejo-trigger
       │ list PoudriereBulks in <namespace>;
       │ filter by annotation forgejo.nodemanager/repo == <pushed repo>;
       │ patch annotation freebsd.nodemanager/trigger = <commit SHA>
       ▼
PoudriereBulk reconcile fires (annotation changed → input hash differs)
       │
       ▼
Existing Command executor dispatches the build via the bridge scripts
```

For the case where the workflow YAML lives in the same repo as the
ports tree, you don't need this service — Forgejo's native `on:push:`
fires the workflow directly. The trigger service exists for the
split-repo case (workflow in a build-infra repo, ports in a
personal-ports repo).

## Architecture

| Component | Lives in | Responsibility |
|---|---|---|
| `pkg/forgejotrigger` | This repo | HTTP handler, HMAC verification, k8s patch logic. 86%+ test coverage. |
| `cmd/forgejo-trigger` | This repo | Thin main: flag parsing, k8s client construction, server start with graceful shutdown. |
| `config/forgejo-trigger/manifests.yaml` | This repo | k8s Deployment + ServiceAccount + Role + RoleBinding + Service. Single-namespace, single-replica. |
| Webhook secret | k8s Secret in cluster | HMAC key shared with each Forgejo webhook config. |
| Forgejo webhook | Configured per repo on the Forgejo instance | Sends POST to the trigger's `/webhook` endpoint on push. |
| `forgejo.nodemanager/repo` annotation | On each PoudriereBulk | Selector — declares "this Bulk should rebuild on pushes to <repo>." |

The service is intentionally tiny: ~250 LOC of HTTP handler with no
external dependencies beyond what the controller already uses.

## Setup

### 1. Generate the shared secret

```sh
openssl rand -hex 32
# → e.g. 8f3a4d5e1b9c2f6e0d7a8b9c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e
```

Put this somewhere safe — you'll use it in two places: the k8s Secret
and the Forgejo webhook config. Rotation requires updating both
**and restarting the trigger Pod** (the secret is read once at
startup; the Deployment's `forgejo-trigger.nodemanager/rotation-marker`
annotation is the convention for forcing a rolling restart).

### 2. Create the k8s Secret

```sh
kubectl create secret generic forgejo-trigger-hmac \
  -n nodemanager \
  --from-literal=secret='<the-shared-secret>'
```

### 3. Deploy the service

```sh
kubectl apply -f config/forgejo-trigger/manifests.yaml
```

Edit the image reference (`image: ghcr.io/zachfi/forgejo-trigger:vX.Y.Z`)
to point at wherever you publish builds of this repo.

### 4. Annotate matching PoudriereBulks

For each Bulk that should rebuild on pushes to a particular repo:

```yaml
apiVersion: freebsd.nodemanager/v1
kind: PoudriereBulk
metadata:
  name: personal-amd64
  namespace: nodemanager
  annotations:
    forgejo.nodemanager/repo: zachfi/personal-ports
spec:
  jail: 14amd64
  tree: personal
  ports: [shells/zsh, net/curl]
  reconcilePeriod: 6h
  executor:
    type: Command
    command:
      dispatch: ["/usr/local/libexec/poudriere-dispatch-forgejo.sh"]
      status:   ["/usr/local/libexec/poudriere-status-forgejo.sh"]
      env:
        - { name: FORGEJO_BASE_URL, value: https://forgejo.example.com }
        - { name: FORGEJO_REPO,     value: zachfi/build-infra }
        - { name: FORGEJO_WORKFLOW, value: poudriere-build.yml }
      secretEnv:
        - name: FORGEJO_TOKEN
          secretRef: { name: forgejo-poudriere-pat, key: token }
```

Multiple Bulks can share a repo annotation — every Bulk pointing at
`zachfi/personal-ports` rebuilds on every push to that repo.

### 5. Configure the Forgejo webhook

In the source repo (`zachfi/personal-ports` in the example above):

1. **Settings → Webhooks → Add Webhook → Forgejo**
2. **Target URL**: `https://forgejo-trigger.example.com/webhook`
3. **HTTP Method**: POST
4. **POST Content Type**: `application/json`
5. **Secret**: paste the shared secret
6. **Trigger On**: Push events (uncheck the rest)
7. **Branch filter**: (optional) leave blank for all branches; the
   trigger doesn't filter on ref currently
8. Save and use **Test Delivery** to send a ping. The Recent
   Deliveries pane should show a `200 OK`.

## Verification

After setup, push a commit to the ports tree:

```sh
cd ~/Code/personal-ports
echo "# bumping" >> README.md
git commit -am "bump"
git push
```

Then watch the matching Bulk's status:

```sh
kubectl describe poudrierebulk personal-amd64 -n nodemanager
```

Within a few seconds you should see the trigger annotation pick up
the new SHA, then a few more seconds later the controller's
reconcile fires (rate-limited at 1 token / 30s). Status fields
populate in this order:

1. Annotation `freebsd.nodemanager/trigger` set to the commit SHA
2. `lastDispatchTime` populates
3. `lastDispatchedRunID` populates with the Forgejo run number
4. `lastDispatchedRunURL` populates with the run page URL
5. (eventually) `lastBuildResult: Succeeded` and the `Available`
   condition flips true

The webhook's own logs (`kubectl logs deployment/forgejo-trigger -n nodemanager`)
record the matched Bulk count per delivery, the SHA, and the Forgejo
delivery UUID for correlation against the Forgejo Recent Deliveries
pane.

## Operational notes

### Idempotency

The same SHA pushed twice is harmless — re-patching with the same
value is a no-op write at the API server (no resourceVersion change,
no watch event), so a duplicate webhook delivery does not double-
trigger. Forgejo retries on 5xx but accepts our 200/202; idempotency
makes any accidental retry safe.

### Failure semantics

| Scenario | Response | Forgejo behaviour |
|---|---|---|
| Valid push, matched Bulk | 200 with body | Success |
| Valid push, no matching Bulk | 200 with `matched=0` | Success |
| Ping event | 200 | Success |
| Non-push event (issues, PR, …) | 202 Accepted | Success (intentional ignore) |
| Missing/invalid HMAC | 401 Unauthorized | Retry — investigate signature config |
| Malformed JSON | 400 Bad Request | Retry — investigate payload |
| k8s API error during list/patch | 500 Internal Server Error | Retry — investigate cluster |

The 202 path on unrelated events is deliberate: Forgejo retries on
4xx/5xx but accepts 2xx as "delivered." Returning 202 says
"got it, intentionally ignored" without triggering retry storms.

### Branch filtering

The current implementation does not filter on git ref. Every push
event for a matched repo bumps the trigger annotation, regardless of
which branch was pushed to. If you only want to rebuild on `main`,
configure the Forgejo webhook's branch filter on its side.

A future enhancement could honour a per-Bulk
`forgejo.nodemanager/branch` annotation; not implemented yet.

### Cluster-wide install

The default manifests are namespace-scoped. To watch all namespaces,
substitute `ClusterRole` + `ClusterRoleBinding` for `Role` +
`RoleBinding`, drop the namespace from the `--namespace` flag (the
binary doesn't currently support cluster-wide; this would need a
small code change to skip `client.InNamespace(...)`).

### Secret rotation

1. Generate a new HMAC value.
2. Update the Forgejo webhook config with the new secret.
3. `kubectl create secret generic forgejo-trigger-hmac --from-literal=secret='<new>' --dry-run=client -o yaml | kubectl apply -f -`
4. Update the `forgejo-trigger.nodemanager/rotation-marker`
   annotation on the Deployment (or rely on `kubectl rollout restart`)
   to force a Pod restart that picks up the new secret.

## Related

- [Command Executor Contract](command-contract.md) — how the trigger
  annotation feeds into the Bulk's input hash.
- [Bridge Security](../security/poudriere-bridges.md) — the threat
  model for the dispatch/status side; HMAC verification on this
  side is the analogous protection.
- [Monitoring Poudriere Builds](../monitoring/poudriere.md) —
  PromQL recipes for confirming a triggered build actually fired.
