package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// nodeServiceAccountPrefix is the naming convention for node SAs.
	nodeServiceAccountPrefix = "system:serviceaccount:nodemanager:nodemanager-"
)

// NodeValidator rejects spec mutations from node ServiceAccounts.
// Non-node-SA requests are always allowed.
type NodeValidator struct {
	decoder admission.Decoder
}

func NewNodeValidator(decoder admission.Decoder) *NodeValidator {
	return &NodeValidator{decoder: decoder}
}

func (v *NodeValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	// Only validate UPDATE operations — CREATE/DELETE are blocked by RBAC.
	if req.Operation != admissionv1.Update {
		return admission.Allowed("")
	}

	hostname := nodeHostname(req.UserInfo.Username)
	if hostname == "" {
		return admission.Allowed("")
	}

	oldSpec, newSpec, err := extractSpecs(req)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding objects: %w", err))
	}

	// Reject spec changes from node SAs.
	if !jsonEqual(oldSpec, newSpec) {
		return admission.Denied(fmt.Sprintf(
			"node SA %q is not permitted to modify .spec", req.UserInfo.Username))
	}

	// For Jail resources, verify the node only touches its own jails.
	if req.Resource.Resource == "jails" {
		nodeName, err := extractJailNodeName(req)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if nodeName != "" && nodeName != hostname {
			return admission.Denied(fmt.Sprintf(
				"node SA %q cannot modify jail assigned to %q", req.UserInfo.Username, nodeName))
		}
	}

	// For ManagedNode resources, verify the node only touches its own.
	if req.Resource.Resource == "managednodes" {
		if req.Name != hostname {
			return admission.Denied(fmt.Sprintf(
				"node SA %q cannot modify ManagedNode %q", req.UserInfo.Username, req.Name))
		}
	}

	return admission.Allowed("")
}

// nodeHostname extracts the hostname from a node SA username.
// Returns empty string if the username is not a node SA.
func nodeHostname(username string) string {
	if !strings.HasPrefix(username, nodeServiceAccountPrefix) {
		return ""
	}
	return strings.TrimPrefix(username, nodeServiceAccountPrefix)
}

// extractSpecs pulls the .spec field from the old and new objects as raw JSON.
func extractSpecs(req admission.Request) (oldSpec, newSpec json.RawMessage, err error) {
	var oldObj, newObj map[string]json.RawMessage

	if err := json.Unmarshal(req.Object.Raw, &newObj); err != nil {
		return nil, nil, fmt.Errorf("unmarshalling new object: %w", err)
	}
	if err := json.Unmarshal(req.OldObject.Raw, &oldObj); err != nil {
		return nil, nil, fmt.Errorf("unmarshalling old object: %w", err)
	}

	return oldObj["spec"], newObj["spec"], nil
}

// extractJailNodeName reads spec.nodeName from the new object.
func extractJailNodeName(req admission.Request) (string, error) {
	var obj struct {
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(req.Object.Raw, &obj); err != nil {
		return "", fmt.Errorf("reading jail spec.nodeName: %w", err)
	}
	return obj.Spec.NodeName, nil
}

// jsonEqual compares two JSON values for semantic equality.
func jsonEqual(a, b json.RawMessage) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}

	aj, _ := json.Marshal(av)
	bj, _ := json.Marshal(bv)
	return string(aj) == string(bj)
}
