/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BulkExecutorType selects how a PoudriereBulk's build is performed.
//
// +kubebuilder:validation:Enum=InProcess;Command
type BulkExecutorType string

const (
	// BulkExecutorInProcess runs `poudriere bulk` directly inside the
	// nodemanager process on the host where the reconciler is running.
	// This is the default and matches v0.14.x behaviour.
	BulkExecutorInProcess BulkExecutorType = "InProcess"

	// BulkExecutorCommand invokes an external program — shell script, Go
	// binary, anything os/exec can run — to dispatch the build to a
	// separate build system (Forgejo Actions, Woodpecker, a remote shell,
	// …).  See docs/poudriere/command-contract.md for the stdio + JSON
	// contract executors must satisfy.
	BulkExecutorCommand BulkExecutorType = "Command"
)

// BulkExecutor selects and configures how a PoudriereBulk is executed.
// The default (Type unset) is InProcess for backward compatibility.
type BulkExecutor struct {
	// Type selects the executor.  See the BulkExecutorType constants.
	// +kubebuilder:default=InProcess
	Type BulkExecutorType `json:"type,omitempty"`

	// Command configures the external program when Type=Command.  Ignored
	// for other Types.  See CommandExecutor for the contract.
	// +optional
	Command *CommandExecutor `json:"command,omitempty"`
}

// CommandExecutor configures an external program that brokers between
// the PoudriereBulk CR and an off-cluster build system (typically a CI
// runner such as Forgejo Actions or Woodpecker).
//
// The program receives the bulk's spec on stdin as a JSON
// BulkDispatchInput document and returns an opaque run identifier on
// stdout.  An optional Status program polls in-flight runs and returns a
// BulkRunStatus JSON object.  The full contract is documented in
// docs/poudriere/command-contract.md and represented in code by the
// types in pkg/cmdrunner.
//
// Programs may be shell scripts, compiled binaries, or any other
// executable on the host.  The CRD does not constrain the executable
// shape; nodemanager invokes it via os/exec.
type CommandExecutor struct {
	// Dispatch is the executable + arguments invoked when starting a
	// build.  argv[0] must be an absolute path (no $PATH lookup at the
	// CR level — operators put the program where they want and reference
	// it explicitly).  See docs/poudriere/command-contract.md.
	// +kubebuilder:validation:MinItems=1
	Dispatch []string `json:"dispatch"`

	// Status is the executable + arguments invoked when polling an
	// in-flight run.  Optional; when empty, dispatched runs are
	// fire-and-forget — nodemanager records LastDispatchedRunID but
	// never queries state.
	// +optional
	Status []string `json:"status,omitempty"`

	// Env is explicit environment passed to Dispatch and Status.  Layers
	// over (and replaces matching keys from) the allowlisted host
	// environment.  Use SecretEnv for credentials.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// SecretEnv exposes Secret keys as environment variables to the
	// executor program.  Resolved at every reconcile (so Secret rotation
	// is automatic) and never written to disk or logged by name.  This
	// is where credentials such as a Forgejo personal access token live.
	// +optional
	SecretEnv []CommandSecretEnv `json:"secretEnv,omitempty"`

	// DispatchTimeout caps the dispatch invocation.  Parsed by
	// time.ParseDuration; defaults to 60s when empty.
	// +optional
	DispatchTimeout string `json:"dispatchTimeout,omitempty"`

	// StatusTimeout caps the status invocation.  Parsed by
	// time.ParseDuration; defaults to 30s when empty.
	// +optional
	StatusTimeout string `json:"statusTimeout,omitempty"`
}

// CommandSecretEnv binds a Secret key to an environment variable the
// executor program receives.
type CommandSecretEnv struct {
	// Name is the environment variable name set in the executor's
	// process environment (e.g. "FORGEJO_TOKEN").  Must be a valid
	// POSIX env var identifier.
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	Name string `json:"name"`

	// SecretRef points to the Secret + key whose value populates Name.
	// The Secret must exist in the same namespace as the PoudriereBulk.
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// PoudriereBulkSpec defines the desired state of PoudriereBulk.
type PoudriereBulkSpec struct {
	// Ports is the list of port origins (e.g. "net/curl", "shells/zsh") to
	// build. Each entry is passed as a separate argument to `poudriere bulk`.
	// +optional
	Ports []string `json:"ports,omitempty"`

	// Tree is the poudriere ports tree name (the -p argument). Must match
	// the metadata.name of a PoudrierePorts in the same namespace.
	// +required
	Tree string `json:"tree"`

	// Jail is the poudriere build jail name (the -j argument). Must match
	// the metadata.name of a PoudriereJail in the same namespace.
	// +required
	Jail string `json:"jail"`

	// ReconcilePeriod controls how often the controller re-runs portshaker
	// and `poudriere bulk` even without a Kubernetes event.  Use this to
	// turn the controller into a scheduled build loop.  Examples: "1h",
	// "6h", "24h".  Empty or zero means event-driven only — a build runs
	// only when the spec changes or an external trigger fires (such as a
	// Forgejo push annotation, see Phase 4).
	// +optional
	ReconcilePeriod string `json:"reconcilePeriod,omitempty"`

	// Executor selects how the build is performed.  When unset the bulk
	// runs in-process on the reconciler's host (backward-compatible with
	// v0.14.x).  Set Executor.Type=Command to dispatch the build to a
	// separate build system via an external program; see CommandExecutor
	// and docs/poudriere/command-contract.md.
	// +optional
	Executor *BulkExecutor `json:"executor,omitempty"`
}

// PoudriereBulkStatus defines the observed state of PoudriereBulk.
type PoudriereBulkStatus struct {
	// Conditions represent the current state of the PoudriereBulk resource.
	//
	// Standard condition types:
	//   - "Available":   the most recent build completed successfully and
	//                    its packages are available in the package repo.
	//   - "Progressing": a build is currently in flight.
	//   - "Degraded":    the most recent build failed.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Hash is a content hash of the build inputs (sorted ports[], tree,
	// jail, plus any external trigger annotation) from the last successful
	// reconcile.  When the recomputed hash matches this value the
	// controller will skip invoking `poudriere bulk` (see Phase 3).  Empty
	// until the first successful build completes.
	// +optional
	Hash string `json:"hash,omitempty"`

	// LastBuildTime records when `poudriere bulk` last completed (success
	// or failure).  Combined with LastBuildResult this lets operators
	// detect stalled or failed builds via Prometheus alerts.
	// +optional
	LastBuildTime *metav1.Time `json:"lastBuildTime,omitempty"`

	// LastBuildResult is "Succeeded" or "Failed" for the most recent run.
	// Empty before the first build completes.
	// +optional
	LastBuildResult string `json:"lastBuildResult,omitempty"`

	// LastError captures the error message from the most recent failed
	// build.  Cleared on a subsequent successful build.
	// +optional
	LastError string `json:"lastError,omitempty"`

	// LastDispatchTime records when the controller last invoked an
	// external dispatch program (Spec.Executor.Type=Command).  Only set
	// for Command executors.  Distinct from LastBuildTime — dispatch is
	// the moment the build was *requested*; LastBuildTime is the moment
	// the controller learned the build *finished*.  For InProcess
	// executors the two coincide.
	// +optional
	LastDispatchTime *metav1.Time `json:"lastDispatchTime,omitempty"`

	// LastDispatchedRunID is the opaque run identifier returned by the
	// most recent successful dispatch (Spec.Executor.Type=Command,
	// Dispatch program exit 0).  Passed back to the Status program when
	// polling.  Empty for InProcess executors.
	// +optional
	LastDispatchedRunID string `json:"lastDispatchedRunID,omitempty"`

	// LastDispatchedRunURL is an optional human-readable URL for the run
	// (e.g. a Forgejo Actions run page).  Reported by the Status program
	// in BulkRunStatus.url.
	// +optional
	LastDispatchedRunURL string `json:"lastDispatchedRunURL,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PoudriereBulk is the Schema for the poudrierebulks API
type PoudriereBulk struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PoudriereBulkSpec   `json:"spec,omitempty"`
	Status PoudriereBulkStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PoudriereBulkList contains a list of PoudriereBulk
type PoudriereBulkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PoudriereBulk `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PoudriereBulk{}, &PoudriereBulkList{})
}
