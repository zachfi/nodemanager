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

// ManagedNodeSpec defines the desired state of ManagedNode
type ManagedNodeSpec struct {
	Domain  string  `json:"domain,omitempty"`
	Upgrade Upgrade `json:"upgrade,omitempty"`
}

type Upgrade struct {
	Group    string `json:"group,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	Delay    string `json:"delay,omitempty"`
}

// ManagedNodeStatus defines the observed state of ManagedNode
type ManagedNodeStatus struct {
	Release string `json:"release,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ManagedNode is the Schema for the managednodes API
type ManagedNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ManagedNodeSpec   `json:"spec,omitempty"`
	Status ManagedNodeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ManagedNodeList contains a list of ManagedNode
type ManagedNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagedNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ManagedNode{}, &ManagedNodeList{})
}
