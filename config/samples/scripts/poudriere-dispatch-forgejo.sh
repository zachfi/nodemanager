#!/bin/sh
# poudriere-dispatch-forgejo.sh
#
# Reference bridge implementing the v1 PoudriereBulk Command executor
# Dispatch contract for Forgejo Actions.  Reads a BulkDispatchInput JSON
# document from stdin, fires a workflow_dispatch on the configured
# Forgejo workflow, and prints the resulting run ID to stdout.
#
# Contract reference: docs/poudriere/command-contract.md
#
# Required environment (typically supplied via the PoudriereBulk CR's
# spec.executor.command.env and .secretEnv fields):
#
#   FORGEJO_BASE_URL   Base URL of the Forgejo instance, e.g.
#                      https://code.znet (no trailing slash)
#   FORGEJO_REPO       owner/name of the repo holding the workflow
#                      (e.g. zachfi/build-infra)
#   FORGEJO_WORKFLOW   Workflow filename (e.g. poudriere-build.yml)
#   FORGEJO_TOKEN      Forgejo personal access token with workflow
#                      dispatch permission.  Comes from a k8s Secret
#                      via spec.executor.command.secretEnv.
#
# Optional environment:
#
#   FORGEJO_REF        Git ref the workflow runs against (default: main)
#   FORGEJO_DISPATCH_DELAY  Seconds to wait between POSTing the dispatch
#                      and querying for the resulting run ID.  Defaults
#                      to 2.  Forgejo's /dispatches endpoint returns
#                      204 No Content with no body, so we have to poll
#                      /runs to recover the run ID; the delay gives
#                      the run record time to materialize.
#
# Exits 0 with the run ID on stdout on success.
# Exits non-zero with a diagnostic on stderr on failure.  The
# controller surfaces stderr into Status.LastError (truncated to 4 KB)
# so the failing message becomes visible via `kubectl describe`.
#
# Script-author hygiene rules — see docs/security/poudriere-bridges.md:
#   * Do not 'set -x' once secrets are in env (pretty much never here).
#   * Do not 'echo $FORGEJO_TOKEN' or include it in error messages.
#   * Prefer short error messages on stderr; the controller may log them.

set -eu

: "${FORGEJO_BASE_URL:?FORGEJO_BASE_URL is required}"
: "${FORGEJO_REPO:?FORGEJO_REPO is required (e.g. zachfi/build-infra)}"
: "${FORGEJO_WORKFLOW:?FORGEJO_WORKFLOW is required (e.g. poudriere-build.yml)}"
: "${FORGEJO_TOKEN:?FORGEJO_TOKEN is required}"
FORGEJO_REF="${FORGEJO_REF:-main}"
FORGEJO_DISPATCH_DELAY="${FORGEJO_DISPATCH_DELAY:-2}"

# Read and parse the BulkDispatchInput document.
SPEC=$(cat)

JAIL=$(printf '%s' "$SPEC" | jq -r '.spec.jail')
TREE=$(printf '%s' "$SPEC" | jq -r '.spec.tree')
PORTS=$(printf '%s' "$SPEC" | jq -r '.spec.ports | join(" ")')
NAME=$(printf '%s' "$SPEC" | jq -r '.metadata.name')

if [ -z "$JAIL" ] || [ "$JAIL" = "null" ]; then
    echo "spec.jail is required in BulkDispatchInput" >&2
    exit 1
fi
if [ -z "$TREE" ] || [ "$TREE" = "null" ]; then
    echo "spec.tree is required in BulkDispatchInput" >&2
    exit 1
fi

# POST workflow_dispatch.  Forgejo returns 204 with no body on success.
DISPATCH_URL="$FORGEJO_BASE_URL/api/v1/repos/$FORGEJO_REPO/actions/workflows/$FORGEJO_WORKFLOW/dispatches"
DISPATCH_BODY=$(jq -nc \
    --arg ref  "$FORGEJO_REF" \
    --arg jail "$JAIL" \
    --arg tree "$TREE" \
    --arg ports "$PORTS" \
    --arg bulk "$NAME" \
    '{ref: $ref, inputs: {jail: $jail, tree: $tree, ports: $ports, bulk: $bulk}}')

if ! ERR=$(curl -fsS -X POST \
    -H "Authorization: token $FORGEJO_TOKEN" \
    -H "Content-Type: application/json" \
    "$DISPATCH_URL" \
    -d "$DISPATCH_BODY" 2>&1); then
    # ERR contains curl's diagnostic; deliberately does NOT include
    # FORGEJO_TOKEN because curl does not print Authorization headers.
    echo "forgejo workflow_dispatch failed: $ERR" >&2
    exit 1
fi

# Forgejo's dispatch endpoint doesn't return the run ID.  Wait briefly
# for the run record to appear, then look up the most recent
# workflow_dispatch run we just kicked off.
sleep "$FORGEJO_DISPATCH_DELAY"

RUNS_URL="$FORGEJO_BASE_URL/api/v1/repos/$FORGEJO_REPO/actions/runs?per_page=10"
if ! RUNS_JSON=$(curl -fsS \
    -H "Authorization: token $FORGEJO_TOKEN" \
    "$RUNS_URL" 2>&1); then
    echo "forgejo runs query failed: $RUNS_JSON" >&2
    exit 1
fi

# Pick the most-recent workflow_dispatch run for the configured workflow.
# Forgejo's run record exposes `path` (e.g. .forgejo/workflows/foo.yml);
# we match on the suffix to allow either bare filename or full path.
RUN_ID=$(printf '%s' "$RUNS_JSON" | jq -r --arg wf "$FORGEJO_WORKFLOW" '
    .workflow_runs
    | map(select(.event == "workflow_dispatch"))
    | map(select((.path // "") | endswith($wf)))
    | sort_by(.created_at)
    | reverse
    | .[0].id // empty
')

if [ -z "$RUN_ID" ]; then
    echo "dispatched but could not determine run ID from /runs (try increasing FORGEJO_DISPATCH_DELAY)" >&2
    exit 1
fi

printf '%s\n' "$RUN_ID"
