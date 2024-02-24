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

// ConfigSetSpec defines the desired state of ConfigSet
type ConfigSetSpec struct {
	Files      []File    `json:"files,omitempty"`
	Packages   []Package `json:"packages,omitempty"`
	Services   []Service `json:"services,omitempty"`
	Executions []Exec    `json:"executions,omitempty"`
}

type Package struct {
	Ensure string `json:"ensure,omitempty"`
	Name   string `json:"name,omitempty"`
}

type Service struct {
	Enable          bool     `json:"enable,omitempty"`
	Ensure          string   `json:"ensure,omitempty"`
	Name            string   `json:"name,omitempty"`
	SusbscribeFiles []string `json:"subscribe_files,omitempty"`
	Arguments       string   `json:"arguments,omitempty"`
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
}

type Exec struct {
	Command         string   `json:"command,omitempty"`
	Args            []string `json:"args,omitempty"`
	SusbscribeFiles []string `json:"subscribe_files,omitempty"`
}

// ConfigSetStatus defines the observed state of ConfigSet
type ConfigSetStatus struct{}

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
