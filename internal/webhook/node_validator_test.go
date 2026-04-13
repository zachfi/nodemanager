package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestNodeValidator(t *testing.T) {
	validator := NewNodeValidator(admission.NewDecoder(runtime.NewScheme()))

	managedNodeResource := metav1.GroupVersionResource{
		Group:    "common.nodemanager",
		Version:  "v1",
		Resource: "managednodes",
	}
	jailResource := metav1.GroupVersionResource{
		Group:    "freebsd.nodemanager",
		Version:  "v1",
		Resource: "jails",
	}

	cases := []struct {
		name    string
		req     admission.Request
		allowed bool
		msg     string
	}{
		{
			name: "non-node SA — allow",
			req: updateRequest(
				"admin-user",
				managedNodeResource,
				"myhost",
				managedNode("myhost", `{"domain":"example.com"}`),
				managedNode("myhost", `{"domain":"changed.com"}`),
			),
			allowed: true,
		},
		{
			name: "node SA, spec unchanged, labels changed — allow",
			req: updateRequest(
				"system:serviceaccount:nodemanager:nodemanager-myhost",
				managedNodeResource,
				"myhost",
				managedNode("myhost", `{"domain":"example.com"}`),
				managedNodeWithLabels("myhost", `{"domain":"example.com"}`, map[string]string{"kubernetes.io/os": "freebsd"}),
			),
			allowed: true,
		},
		{
			name: "node SA, spec changed — deny",
			req: updateRequest(
				"system:serviceaccount:nodemanager:nodemanager-myhost",
				managedNodeResource,
				"myhost",
				managedNode("myhost", `{"domain":"example.com"}`),
				managedNode("myhost", `{"domain":"changed.com"}`),
			),
			allowed: false,
			msg:     "not permitted to modify .spec",
		},
		{
			name: "node SA, different node's ManagedNode — deny",
			req: updateRequest(
				"system:serviceaccount:nodemanager:nodemanager-myhost",
				managedNodeResource,
				"otherhost",
				managedNode("otherhost", `{"domain":"example.com"}`),
				managedNodeWithLabels("otherhost", `{"domain":"example.com"}`, map[string]string{"foo": "bar"}),
			),
			allowed: false,
			msg:     "cannot modify ManagedNode",
		},
		{
			name: "node SA, finalizer change only — allow",
			req: updateRequest(
				"system:serviceaccount:nodemanager:nodemanager-myhost",
				jailResource,
				"myjail",
				jail("myjail", "myhost", `{"nodeName":"myhost","release":"14.2-RELEASE"}`),
				jailWithFinalizer("myjail", "myhost", `{"nodeName":"myhost","release":"14.2-RELEASE"}`),
			),
			allowed: true,
		},
		{
			name: "node SA, jail for different node — deny",
			req: updateRequest(
				"system:serviceaccount:nodemanager:nodemanager-myhost",
				jailResource,
				"otherjail",
				jail("otherjail", "otherhost", `{"nodeName":"otherhost","release":"14.2-RELEASE"}`),
				jailWithFinalizer("otherjail", "otherhost", `{"nodeName":"otherhost","release":"14.2-RELEASE"}`),
			),
			allowed: false,
			msg:     "cannot modify jail assigned to",
		},
		{
			name: "node SA, jail spec changed — deny",
			req: updateRequest(
				"system:serviceaccount:nodemanager:nodemanager-myhost",
				jailResource,
				"myjail",
				jail("myjail", "myhost", `{"nodeName":"myhost","release":"14.2-RELEASE"}`),
				jail("myjail", "myhost", `{"nodeName":"myhost","release":"14.4-RELEASE"}`),
			),
			allowed: false,
			msg:     "not permitted to modify .spec",
		},
		{
			name: "CREATE operation — allow (RBAC handles this)",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: admissionv1.Create,
					UserInfo:  authenticationv1.UserInfo{Username: "system:serviceaccount:nodemanager:nodemanager-myhost"},
				},
			},
			allowed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := validator.Handle(context.Background(), tc.req)
			require.Equal(t, tc.allowed, resp.Allowed, "response: %+v", resp.Result)
			if tc.msg != "" {
				require.Contains(t, resp.Result.Message, tc.msg)
			}
		})
	}
}

func TestNodeHostname(t *testing.T) {
	cases := []struct {
		username string
		want     string
	}{
		{"system:serviceaccount:nodemanager:nodemanager-myhost", "myhost"},
		{"system:serviceaccount:nodemanager:nodemanager-host.example.com", "host.example.com"},
		{"admin", ""},
		{"system:serviceaccount:other:nodemanager-myhost", ""},
	}

	for _, tc := range cases {
		t.Run(tc.username, func(t *testing.T) {
			require.Equal(t, tc.want, nodeHostname(tc.username))
		})
	}
}

// helpers

func updateRequest(username string, resource metav1.GroupVersionResource, name string, oldObj, newObj []byte) admission.Request {
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			UserInfo:  authenticationv1.UserInfo{Username: username},
			Resource:  resource,
			Name:      name,
			Object:    runtime.RawExtension{Raw: newObj},
			OldObject: runtime.RawExtension{Raw: oldObj},
		},
	}
}

func managedNode(name, spec string) []byte {
	return rawObject(name, spec, nil, nil)
}

func managedNodeWithLabels(name, spec string, labels map[string]string) []byte {
	return rawObject(name, spec, labels, nil)
}

func jail(name, _ /* nodeName */, spec string) []byte {
	return rawObject(name, spec, nil, nil)
}

func jailWithFinalizer(name, _ /* nodeName */, spec string) []byte {
	return rawObject(name, spec, nil, []string{"freebsd.nodemanager/finalizer"})
}

func rawObject(name, spec string, labels map[string]string, finalizers []string) []byte {
	obj := map[string]any{
		"metadata": map[string]any{
			"name":       name,
			"labels":     labels,
			"finalizers": finalizers,
		},
		"spec": json.RawMessage(spec),
	}
	data, _ := json.Marshal(obj)
	return data
}
