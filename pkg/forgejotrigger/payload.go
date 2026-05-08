// Package forgejotrigger receives Forgejo push webhooks and converts
// them into trigger-annotation patches on matching PoudriereBulk CRs.
//
// The wider design is documented in
// docs/poudriere/forgejo-trigger.md; this package is the
// implementation.  The cmd/forgejo-trigger binary is the operator-
// facing entrypoint and is intentionally thin: flag parsing, k8s
// client setup, and Server.ListenAndServe.
package forgejotrigger

// PushPayload is the subset of the Forgejo push-event JSON document
// the webhook actually consumes.  Forgejo's full payload is large
// (several KB of pusher/sender/commits/repository metadata); we
// decode only the two fields that drive a build trigger so a future
// upstream addition can't break us.
//
// The shape mirrors GitHub's webhook payload because Forgejo's API
// is GitHub-compatible — both projects send `ref`, `after`, and a
// `repository.full_name` on push.  Other event types (pull_request,
// issues, etc.) are ignored by Server.handleWebhook before
// PushPayload is touched.
type PushPayload struct {
	// Ref is the Git ref that was pushed, e.g. "refs/heads/main".
	// Currently informational; we do not filter on branch.  A future
	// addition could honour a per-Bulk branch selector annotation
	// before patching.
	Ref string `json:"ref"`

	// After is the commit SHA the ref now points at.  This is the
	// value patched into the freebsd.nodemanager/trigger annotation
	// on matching PoudriereBulks.  Empty means "branch deleted" and
	// the webhook ignores the event.
	After string `json:"after"`

	// Repository carries the source repo's identity.
	Repository PushRepository `json:"repository"`
}

// PushRepository is the per-repo data the webhook reads from a push
// event.  Only full_name is used to match; html_url is recorded in
// log lines for human correlation.
type PushRepository struct {
	// FullName is the canonical "owner/name" form, e.g.
	// "zachfi/personal-ports".  Compared against the
	// freebsd.nodemanager.ForgejoRepoAnnotation value on each
	// PoudriereBulk in the configured namespace; matches are bumped.
	FullName string `json:"full_name"`

	// HTMLURL is the repo's web URL.  Logged on accepted webhook
	// requests for operator correlation; not used for matching.
	HTMLURL string `json:"html_url"`
}
