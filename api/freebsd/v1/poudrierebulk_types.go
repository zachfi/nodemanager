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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
