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

// PostCreateCommand describes a command to run inside a jail after its first
// successful start.  Commands are executed sequentially via jexec(8).
type PostCreateCommand struct {
	// Name is a human-readable label for this hook (used in logs and conditions).
	// +required
	Name string `json:"name"`

	// Command is the executable path inside the jail.
	// +required
	Command string `json:"command"`

	// Args are arguments passed to the command.
	// +optional
	Args []string `json:"args,omitempty"`
}

// JailTemplateSpec defines shared defaults that Jails can inherit by
// referencing this template via spec.templateRef.  Jail-level fields always
// take precedence over template defaults.
type JailTemplateSpec struct {
	// Interface is the default network interface for jails using this template.
	// +optional
	Interface string `json:"interface,omitempty"`

	// Mounts defines default filesystem mounts made available inside the jail.
	// +optional
	Mounts []JailMount `json:"mounts,omitempty"`

	// Update defines default freebsd-update settings.
	// +optional
	Update JailUpdate `json:"update,omitempty"`

	// PostCreate holds commands to run inside the jail after its first
	// successful start.  Commands are executed sequentially; if any command
	// fails the remaining commands are skipped and the jail is marked degraded.
	// +optional
	PostCreate []PostCreateCommand `json:"postCreate,omitempty"`

	// PF provides default PF anchor rules for jails using this template.
	// Template rules are prepended to any jail-level rules, allowing the
	// template to establish a base policy (e.g. default-deny) that individual
	// jails extend with service-specific passes.
	// +optional
	PF *JailPF `json:"pf,omitempty"`
}

// +kubebuilder:object:root=true

// JailTemplate is the Schema for the jailtemplates API.
// It provides shared defaults that Jail resources can inherit.
type JailTemplate struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec JailTemplateSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// JailTemplateList contains a list of JailTemplate.
type JailTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []JailTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&JailTemplate{}, &JailTemplateList{})
}
