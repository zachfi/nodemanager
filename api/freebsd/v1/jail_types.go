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

// JailUpdate controls periodic in-place security updates via freebsd-update(8).
// This applies patch-level updates within the declared release.  To move to a
// new release, change spec.release — the controller will reprovision the jail
// root from the new base.
type JailUpdate struct {
	// Schedule is a cron expression for when updates should run.
	Schedule string `json:"schedule,omitempty"`
	// Delay is the minimum time between updates (e.g. "24h").
	Delay string `json:"delay,omitempty"`
	// Group is a lease group name.  Only one jail in the group will run
	// freebsd-update at a time, preventing concurrent disruption across hosts.
	Group string `json:"group,omitempty"`
}

// JailSpec defines the desired state of Jail.
type JailSpec struct {
	// TemplateRef is the name of a JailTemplate in the same namespace.
	// When set, the template's defaults are merged under this spec's values
	// (jail-level fields take precedence over template defaults).
	// +optional
	TemplateRef string `json:"templateRef,omitempty"`

	// NodeName restricts reconciliation to the nodemanager instance running on
	// the named host. If empty the jail is ignored by all nodes.
	// +required
	NodeName string `json:"nodeName"`

	// Release is the FreeBSD release version to use (e.g. "14.2-RELEASE").
	// +required
	Release string `json:"release"`

	// Hostname is the jail's internal hostname. Defaults to the resource name.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// Interface is the network interface to attach to the jail.
	// +optional
	Interface string `json:"interface,omitempty"`

	// Inet is the IPv4 address assigned to the jail (CIDR or bare IP).
	// +optional
	Inet string `json:"inet,omitempty"`

	// Inet6 is the IPv6 address assigned to the jail (CIDR or bare IP).
	// +optional
	Inet6 string `json:"inet6,omitempty"`

	// Mounts defines additional filesystem mounts made available inside the jail
	// via a per-jail fstab file.
	// +optional
	Mounts []JailMount `json:"mounts,omitempty"`

	// Update controls periodic freebsd-update(8) runs for this jail.
	// +optional
	Update JailUpdate `json:"update,omitempty"`

	// DeletionProtection prevents the jail from being deleted when set to true.
	// The controller will block deletion by holding the finalizer until this
	// field is explicitly set to false.  Use this to guard important jails
	// against accidental kubectl delete.
	// +optional
	DeletionProtection bool `json:"deletionProtection,omitempty"`
}

// JailMount describes a single filesystem mount inside the jail.
type JailMount struct {
	// HostPath is the absolute path on the host to expose inside the jail.
	HostPath string `json:"hostPath"`

	// JailPath is the absolute path inside the jail root where HostPath is mounted.
	JailPath string `json:"jailPath"`

	// Type is the filesystem type. Defaults to "nullfs".
	// +optional
	Type string `json:"type,omitempty"`

	// ReadOnly mounts the filesystem read-only.
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`
}

// JailStatus defines the observed state of Jail.
type JailStatus struct {
	// conditions represent the current state of the Jail resource.
	//
	// Standard condition types:
	//   - "Available":   jail is running and healthy.
	//   - "Progressing": jail is being provisioned or updated.
	//   - "Degraded":    jail failed to reach or maintain desired state.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Release is the FreeBSD release string reported by the jail root
	// (from /bin/freebsd-version).  Compare against spec.release to detect
	// when a reprovision has completed or is pending.
	// +optional
	Release string `json:"release,omitempty"`

	// LastUpdate is the time of the last successful freebsd-update run.
	// +optional
	LastUpdate *metav1.Time `json:"lastUpdate,omitempty"`

	// PostCreateDone records when postCreate hooks from the referenced
	// JailTemplate completed successfully.
	// +optional
	PostCreateDone *metav1.Time `json:"postCreateDone,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Jail is the Schema for the jails API.
type Jail struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec JailSpec `json:"spec"`

	// +optional
	Status JailStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// JailList contains a list of Jail.
type JailList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Jail `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Jail{}, &JailList{})
}
