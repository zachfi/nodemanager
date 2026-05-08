#!/bin/sh
# poudriere-status-forgejo.sh
#
# Reference bridge implementing the v1 PoudriereBulk Command executor
# Status contract for Forgejo Actions.  Reads a run ID from stdin
# (one line) and prints a BulkRunStatus JSON document on stdout.
#
# Contract reference: docs/poudriere/command-contract.md
#
# Required environment (typically supplied via the PoudriereBulk CR's
# spec.executor.command.env and .secretEnv fields):
#
#   FORGEJO_BASE_URL   Base URL of the Forgejo instance
#   FORGEJO_REPO       owner/name of the repo holding the workflow
#   FORGEJO_TOKEN      Forgejo personal access token with read access
#                      to actions/runs.  Comes from a k8s Secret via
#                      spec.executor.command.secretEnv.
#
# Exits 0 with a BulkRunStatus JSON object on stdout.
# Exits non-zero on transient/infra failure; the controller logs and
# retries on the next reconcile.  A malformed run ID or missing run is
# treated as 'unknown' so the controller continues polling rather than
# erroring out.
#
# Translation table — Forgejo run.status × run.conclusion → state:
#
#   status="completed", conclusion="success"     → success
#   status="completed", conclusion="failure"     → failed
#   status="completed", conclusion="cancelled"   → failed
#   status="completed", conclusion="skipped"     → failed
#   status="completed", conclusion=anything else → failed
#   status=anything else (queued, in_progress)   → running

set -eu

: "${FORGEJO_BASE_URL:?FORGEJO_BASE_URL is required}"
: "${FORGEJO_REPO:?FORGEJO_REPO is required}"
: "${FORGEJO_TOKEN:?FORGEJO_TOKEN is required}"

RUN_ID=$(head -n 1 | tr -d '[:space:]')

if [ -z "$RUN_ID" ]; then
    echo "no run ID on stdin" >&2
    exit 1
fi

# Validate it looks like a positive integer so we don't shove user
# input into a URL path unsanitised.
case "$RUN_ID" in
    ''|*[!0-9]*)
        echo "invalid run ID: $RUN_ID" >&2
        exit 1
        ;;
esac

RUN_URL="$FORGEJO_BASE_URL/api/v1/repos/$FORGEJO_REPO/actions/runs/$RUN_ID"
if ! RUN_JSON=$(curl -fsS \
    -H "Authorization: token $FORGEJO_TOKEN" \
    "$RUN_URL" 2>&1); then
    echo "forgejo runs query failed: $RUN_JSON" >&2
    exit 1
fi

# Translate Forgejo's status/conclusion into the contract's BulkRunStatus.
printf '%s' "$RUN_JSON" | jq '{
    apiVersion: "freebsd.nodemanager/v1",
    kind: "BulkRunStatus",
    state: (
        if .status == "completed" then
            (if .conclusion == "success" then "success" else "failed" end)
        else "running"
        end
    ),
    url: (.html_url // ""),
    error: (
        if .status == "completed" and .conclusion != "success" then
            ("forgejo run \(.conclusion // "unknown")")
        else ""
        end
    )
}'
