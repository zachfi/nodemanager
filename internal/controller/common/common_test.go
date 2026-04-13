package common

import (
	"testing"
)

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
