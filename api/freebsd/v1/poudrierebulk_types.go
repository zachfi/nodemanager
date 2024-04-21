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

// PoudriereBulkSpec defines the desired state of PoudriereBulk
type PoudriereBulkSpec struct {
	Ports []string `json:"ports,omitempty"`
	Tree  string   `json:"tree,omitempty"`
	Jail  string   `json:"jail,omitempty"`
}

// PoudriereBulkStatus defines the observed state of PoudriereBulk
type PoudriereBulkStatus struct {
	Hash string `json:"hash,omitempty"`
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
