/*
Copyright 2022.

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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PoudriereJailSpec defines the desired state of PoudriereJail
type PoudriereJailSpec struct {
	Version      string `json:"version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Makeopts     string `json:"makeopts,omitempty"`
}

// PoudriereJailStatus defines the observed state of PoudriereJail
type PoudriereJailStatus struct {
	Version      string `json:"version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Mountpoint   string `json:"mountpoint,omitempty"`
	FetchMethod  string `json:"fetchmethod,omitempty"`
	CreationDate string `json:"creationdate,omitempty"`
	CreationTime string `json:"creationtime,omitempty"`
	MakeoptsHash string `json:"makeopts,omitempty"`
	Ready        bool   `json:"ready,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PoudriereJail is the Schema for the poudrierejails API
type PoudriereJail struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PoudriereJailSpec   `json:"spec,omitempty"`
	Status PoudriereJailStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PoudriereJailList contains a list of PoudriereJail
type PoudriereJailList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PoudriereJail `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PoudriereJail{}, &PoudriereJailList{})
}
