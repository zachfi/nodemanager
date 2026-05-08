// Package cmdrunner is the implementation of the PoudriereBulk Command
// executor contract.  See docs/poudriere/command-contract.md for the
// authoritative specification; this file's types ARE the contract on
// the Go side.
//
// The contract in two sentences: nodemanager invokes an external
// program with a JSON BulkDispatchInput on stdin, expects an opaque
// run-ID line on stdout; an optional Status program receives the run-ID
// on stdin and emits a JSON BulkRunStatus on stdout.  Versioning is
// carried in the apiVersion and kind discriminators on every JSON
// document.
package cmdrunner

// ContractAPIVersion is the apiVersion string stamped on every JSON
// document exchanged with executor programs.  Bump only on incompatible
// schema changes; additive field changes happen within the same
// version with the forward-compatibility rule that executors ignore
// unknown fields.
const ContractAPIVersion = "freebsd.nodemanager/v1"

// Kind constants for the JSON discriminators.  Executors that strictly
// validate input/output should compare against these.
const (
	KindBulkDispatchInput = "BulkDispatchInput"
	KindBulkRunStatus     = "BulkRunStatus"
)

// BulkDispatchInput is the JSON document delivered on stdin to a
// Command executor's Dispatch program.  It is the stable representation
// of the PoudriereBulk fields an executor needs to trigger a build.
//
// Forward-compatibility rule: future versions MAY add fields; executor
// programs MUST ignore unknown fields.  Backward-compatibility promise:
// every field present in the v1 schema will continue to be sent.
type BulkDispatchInput struct {
	// APIVersion identifies the contract version.  Always
	// ContractAPIVersion for v1 documents.
	APIVersion string `json:"apiVersion"`

	// Kind identifies the document type.  Always KindBulkDispatchInput
	// for dispatch inputs.
	Kind string `json:"kind"`

	// Metadata carries identity fields from the PoudriereBulk so
	// executors can correlate logs and dispatched runs without parsing
	// out-of-band data.
	Metadata BulkDispatchMetadata `json:"metadata"`

	// Spec is the build's input data.  All fields here come straight
	// from PoudriereBulkSpec.
	Spec BulkDispatchSpec `json:"spec"`
}

// BulkDispatchMetadata is the subset of ObjectMeta executor programs
// can rely on.  The full metadata is intentionally NOT serialised —
// status fields, timestamps, finalizers, and managed-fields are
// orthogonal to dispatch.
type BulkDispatchMetadata struct {
	// Name is the PoudriereBulk's metadata.name.
	Name string `json:"name"`
	// Namespace is the PoudriereBulk's metadata.namespace.
	Namespace string `json:"namespace"`
	// UID is the PoudriereBulk's metadata.uid; useful when an executor
	// wants to tag logs or external-system resources with a stable
	// per-CR identifier.
	UID string `json:"uid"`
	// Generation is the PoudriereBulk's metadata.generation at the
	// moment of dispatch.  Operators can correlate "build for
	// generation N" with their own audit trail.
	Generation int64 `json:"generation"`
}

// BulkDispatchSpec is the build's intent: what to build, where.  This
// is intentionally a flat copy of PoudriereBulkSpec's build-relevant
// fields.  Operational fields (ReconcilePeriod, Executor) are NOT
// included — they configure how the controller behaves, not what the
// executor does.
type BulkDispatchSpec struct {
	// Jail is the poudriere build-jail name.
	Jail string `json:"jail"`
	// Tree is the poudriere ports-tree name.
	Tree string `json:"tree"`
	// Ports is the list of port origins to build.
	Ports []string `json:"ports"`
}

// BulkRunStatus is the JSON document a Command executor's Status
// program emits on stdout when polling an in-flight or completed run.
//
// Forward-compatibility rule: the controller MUST ignore unknown
// fields.  Backward-compatibility promise: every field present in the
// v1 schema will continue to be honoured.
type BulkRunStatus struct {
	// APIVersion identifies the contract version.  Always
	// ContractAPIVersion for v1 documents.
	APIVersion string `json:"apiVersion"`

	// Kind identifies the document type.  Always KindBulkRunStatus for
	// status documents.
	Kind string `json:"kind"`

	// State is the current state of the dispatched run.  See
	// BulkRunState for valid values.
	State BulkRunState `json:"state"`

	// URL is an optional human-readable URL where an operator can view
	// the run (e.g. a Forgejo Actions run page).  Surfaced into
	// PoudriereBulk.Status.LastDispatchedRunURL when non-empty so it
	// shows up in `kubectl describe`.
	// +optional
	URL string `json:"url,omitempty"`

	// Error is an optional human-readable error message.  Set when
	// State=BulkRunStateFailed; ignored otherwise.
	// +optional
	Error string `json:"error,omitempty"`
}

// BulkRunState enumerates the run states the controller acts on.
// "unknown" is reserved for executors that genuinely cannot determine
// the state (e.g. the upstream system has lost the run); the
// controller treats unknown the same as running and retries on the
// next reconcile.
type BulkRunState string

const (
	BulkRunStateRunning BulkRunState = "running"
	BulkRunStateSuccess BulkRunState = "success"
	BulkRunStateFailed  BulkRunState = "failed"
	BulkRunStateUnknown BulkRunState = "unknown"
)

// IsTerminal reports whether the run has reached a terminal state and
// no further status polling is needed.
func (s BulkRunState) IsTerminal() bool {
	return s == BulkRunStateSuccess || s == BulkRunStateFailed
}
