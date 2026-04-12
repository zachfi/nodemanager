package common

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
)

func configSetWithLabels(labels map[string]string) client.Object {
	return &commonv1.ConfigSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Labels:    labels,
		},
	}
}

func TestMatchAllLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		matchers map[string]string
		want     bool
	}{
		{
			name:     "empty matchers returns false",
			labels:   map[string]string{"a": "1"},
			matchers: map[string]string{},
			want:     false,
		},
		{
			name:     "nil matchers returns false",
			labels:   map[string]string{"a": "1"},
			matchers: nil,
			want:     false,
		},
		{
			name:     "exact match",
			labels:   map[string]string{"a": "1", "b": "2"},
			matchers: map[string]string{"a": "1", "b": "2"},
			want:     true,
		},
		{
			name:     "matchers subset of labels",
			labels:   map[string]string{"a": "1", "b": "2", "c": "3"},
			matchers: map[string]string{"a": "1"},
			want:     true,
		},
		{
			name:     "matcher key missing from labels",
			labels:   map[string]string{"a": "1"},
			matchers: map[string]string{"b": "2"},
			want:     false,
		},
		{
			name:     "matcher value mismatch",
			labels:   map[string]string{"a": "1"},
			matchers: map[string]string{"a": "2"},
			want:     false,
		},
		{
			name:     "matchers superset of labels returns false",
			labels:   map[string]string{"a": "1"},
			matchers: map[string]string{"a": "1", "b": "2"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchAllLabels(tt.labels, tt.matchers)
			if got != tt.want {
				t.Errorf("matchAllLabels(%v, %v) = %v, want %v", tt.labels, tt.matchers, got, tt.want)
			}
		})
	}
}

func TestNodeLabelMatchPredicate(t *testing.T) {
	// Simulate a node with os, arch, hostname, and a role label —
	// the full label set that a ManagedNode would have.
	nodeLabels := map[string]string{
		"kubernetes.io/os":             "arch",
		"kubernetes.io/arch":           "amd64",
		"kubernetes.io/hostname":       "vor",
		"role.nodemanager/workstation": "true",
	}
	pred := newNodeLabelMatchPredicate(nodeLabels)

	tests := []struct {
		name      string
		objLabels map[string]string
		wantAdmit bool
	}{
		{
			name:      "configset matching os only",
			objLabels: map[string]string{"kubernetes.io/os": "arch"},
			wantAdmit: true,
		},
		{
			name:      "configset matching os and role",
			objLabels: map[string]string{"kubernetes.io/os": "arch", "role.nodemanager/workstation": "true"},
			wantAdmit: true,
		},
		{
			name:      "configset for different os",
			objLabels: map[string]string{"kubernetes.io/os": "freebsd"},
			wantAdmit: false,
		},
		{
			name:      "configset with label not on node",
			objLabels: map[string]string{"nodemanager/os": "freebsd"},
			wantAdmit: false,
		},
		{
			name:      "unlabeled configset",
			objLabels: map[string]string{},
			wantAdmit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := configSetWithLabels(tt.objLabels)

			got := pred.Create(event.CreateEvent{Object: obj})
			if got != tt.wantAdmit {
				t.Errorf("Create predicate = %v, want %v", got, tt.wantAdmit)
			}

			got = pred.Update(event.UpdateEvent{ObjectNew: obj})
			if got != tt.wantAdmit {
				t.Errorf("Update predicate = %v, want %v", got, tt.wantAdmit)
			}

			got = pred.Delete(event.DeleteEvent{Object: obj})
			if got != tt.wantAdmit {
				t.Errorf("Delete predicate = %v, want %v", got, tt.wantAdmit)
			}

			got = pred.Generic(event.GenericEvent{Object: obj})
			if got != tt.wantAdmit {
				t.Errorf("Generic predicate = %v, want %v", got, tt.wantAdmit)
			}
		})
	}
}

// TestPredicateWithDefaultLabelsOnly verifies that when only DefaultLabels are
// available (no ManagedNode fetched), a ConfigSet requiring a role label is
// incorrectly filtered out. This was the bug: DefaultLabels only has os/arch/hostname,
// so ConfigSets matching on additional labels like role.nodemanager/workstation
// would never be admitted.
func TestPredicateWithDefaultLabelsOnly(t *testing.T) {
	// Simulate DefaultLabels — only os, arch, hostname.
	defaultOnly := map[string]string{
		"kubernetes.io/os":       "arch",
		"kubernetes.io/arch":     "amd64",
		"kubernetes.io/hostname": "vor",
	}
	pred := newNodeLabelMatchPredicate(defaultOnly)

	// A ConfigSet that requires a role label — this would be wrongly rejected
	// when only default labels are used for the predicate.
	obj := configSetWithLabels(map[string]string{
		"kubernetes.io/os":             "arch",
		"role.nodemanager/workstation": "true",
	})

	got := pred.Create(event.CreateEvent{Object: obj})
	if got != false {
		t.Error("with default labels only, predicate should reject configset requiring role label")
	}

	// This demonstrates the bug: the predicate correctly rejects the ConfigSet,
	// but it SHOULD have admitted it because the real node does have the role label.
	// The fix is to use the ManagedNode's full labels instead of DefaultLabels.
}
