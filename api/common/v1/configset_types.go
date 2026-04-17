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

// NotifyRef identifies a Kubernetes resource that should be enqueued for
// reconciliation after this ConfigSet is successfully applied. The controller
// touches a generation-scoped annotation on the target, which triggers that
// resource's own controller without causing a reconcile loop.
type NotifyRef struct {
	// APIVersion is the group/version of the resource, e.g. "freebsd.nodemanager/v1".
	APIVersion string `json:"apiVersion"`
	// Kind is the kind of the resource, e.g. "Jail".
	Kind string `json:"kind"`
	// Name is the specific resource to notify. When empty, all resources of
	// this kind in the namespace are notified.
	// +optional
	Name string `json:"name,omitempty"`
	// Namespace defaults to the ConfigSet's own namespace when omitted.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ConfigSetSpec defines the desired state of ConfigSet
type ConfigSetSpec struct {
	Files      []File    `json:"files,omitempty"`
	Packages   []Package `json:"packages,omitempty"`
	Services   []Service `json:"services,omitempty"`
	Executions []Exec    `json:"executions,omitempty"`
	// Notifies lists resources to reconcile after this ConfigSet is applied.
	// +optional
	Notifies []NotifyRef `json:"notifies,omitempty"`
}

type Package struct {
	Ensure  string `json:"ensure,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type Service struct {
	Enable          bool     `json:"enable,omitempty"`
	Ensure          string   `json:"ensure,omitempty"`
	Name            string   `json:"name,omitempty"`
	SusbscribeFiles []string `json:"subscribe_files,omitempty"`
	Arguments       string   `json:"arguments,omitempty"`
	User            string   `json:"user,omitempty"`
	LockGroup       string   `json:"lock_group,omitempty"`
}

type File struct {
	Content       string   `json:"content,omitempty"`
	Ensure        string   `json:"ensure,omitempty"`
	Target        string   `json:"target,omitempty"`
	Group         string   `json:"group,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	Owner         string   `json:"owner,omitempty"`
	Path          string   `json:"path,omitempty"`
	Template      string   `json:"template,omitempty"`
	SecretRefs    []string `json:"secretRefs,omitempty"`
	ConfigMapRefs []string `json:"configMapRefs,omitempty"`
	// CreateOnly skips writing the file if it already exists on disk.
	// Useful for seed/skeleton files (e.g. ~/.zshrc) that nodemanager should
	// create on first boot but never overwrite afterward.
	CreateOnly bool `json:"createOnly,omitempty"`
	// Purge removes files beneath this path that are not declared in any
	// ConfigSet matching this node. Only meaningful when ensure is "directory".
	// Subdirectories are never removed, only plain files.
	Purge bool `json:"purge,omitempty"`
}

type Exec struct {
	Command         string   `json:"command,omitempty"`
	Args            []string `json:"args,omitempty"`
	SusbscribeFiles []string `json:"subscribe_files,omitempty"`
}

// ConfigSetStatus defines the observed state of ConfigSet
type ConfigSetStatus struct {
	// Conditions includes a Conflicted condition when a resource overlap is detected
	// on the node this controller manages.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ConfigSet is the Schema for the configsets API
type ConfigSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigSetSpec   `json:"spec,omitempty"`
	Status ConfigSetStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConfigSetList contains a list of ConfigSet
type ConfigSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConfigSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConfigSet{}, &ConfigSetList{})
}
