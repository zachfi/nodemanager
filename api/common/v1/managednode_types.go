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

// WireGuardSpec controls optional WireGuard key bootstrapping for this node.
// When Enabled is true, the controller generates a Curve25519 keypair on first
// reconcile and stores it in a Secret named wg-<Interface>-<nodeName>.  The
// public key is published to status.wireGuard so other nodes can build peer
// configs from template data before the interface is configured.
type WireGuardSpec struct {
	// Enabled turns on key bootstrapping for this node.
	Enabled bool `json:"enabled,omitempty"`
	// Interface is the WireGuard interface name to bootstrap.  Defaults to wg0.
	Interface string `json:"interface,omitempty"`
}

// ManagedNodeSpec defines the desired state of ManagedNode
type ManagedNodeSpec struct {
	Domain    string        `json:"domain,omitempty"`
	Upgrade   Upgrade       `json:"upgrade,omitempty"`
	WireGuard WireGuardSpec `json:"wireGuard,omitempty"`
}

type Upgrade struct {
	// TODO: move group to the label.  This makes querying easier since we can
	// filter on label, and we can't filter on a field in the spec.
	Group    string `json:"group,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	Delay    string `json:"delay,omitempty"`
}

// NetworkInterface holds the addresses observed on a single network interface.
type NetworkInterface struct {
	IPv4 []string `json:"ipv4,omitempty"`
	IPv6 []string `json:"ipv6,omitempty"`
}

// ConfigSetApplyStatus records the last reconciliation outcome for a ConfigSet on this node.
type ConfigSetApplyStatus struct {
	Name            string      `json:"name"`
	ResourceVersion string      `json:"resourceVersion,omitempty"`
	LastApplied     metav1.Time `json:"lastApplied,omitempty"`
	Error           string      `json:"error,omitempty"`
	// Conflicts lists resources claimed by both this ConfigSet and another matching
	// ConfigSet, e.g. ["file:/etc/nginx/nginx.conf (also in configset \"web-base\")"].
	// When non-empty, this ConfigSet was not applied on this reconcile.
	Conflicts []string `json:"conflicts,omitempty"`
}

// WireGuardInterface holds the identity information for a WireGuard interface
// on this node. Combined with the node's IP addresses from Interfaces, this
// gives peers everything needed to establish a tunnel.
type WireGuardInterface struct {
	// Name is the network interface name, e.g. wg0.
	Name string `json:"name"`
	// PublicKey is the base64-encoded Curve25519 public key.
	PublicKey string `json:"publicKey"`
	// ListenPort is the UDP port the interface is listening on, if set.
	ListenPort int `json:"listenPort,omitempty"`
}

// SSHHostKey holds the SSHFP record fields for one SSH host key, ready for
// direct insertion into a DNS zone as an SSHFP record (RFC 4255).
type SSHHostKey struct {
	// Algorithm is the SSHFP algorithm number: 1=RSA, 2=DSA, 3=ECDSA, 4=Ed25519.
	Algorithm int `json:"algorithm"`
	// FingerprintType is the SSHFP fingerprint type: 1=SHA-1, 2=SHA-256.
	FingerprintType int `json:"fingerprintType"`
	// Fingerprint is the hex-encoded hash of the public key.
	Fingerprint string `json:"fingerprint"`
}

// ManagedNodeStatus defines the observed state of ManagedNode
type ManagedNodeStatus struct {
	Release     string                      `json:"release,omitempty"`
	Interfaces  map[string]NetworkInterface `json:"interfaces,omitempty"`
	ConfigSets  []ConfigSetApplyStatus      `json:"configsets,omitempty"`
	SSHHostKeys []SSHHostKey                `json:"sshHostKeys,omitempty"`
	WireGuard   []WireGuardInterface        `json:"wireGuard,omitempty"`
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
