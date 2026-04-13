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

func TestSlicesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "nil vs empty", a: nil, b: []string{}, want: true},
		{name: "empty vs nil", a: []string{}, b: nil, want: true},
		{name: "both empty", a: []string{}, b: []string{}, want: true},
		{name: "equal values", a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		{name: "different values", a: []string{"a"}, b: []string{"b"}, want: false},
		{name: "different lengths", a: []string{"a"}, b: []string{"a", "b"}, want: false},
		{name: "one nil one populated", a: nil, b: []string{"a"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slicesEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("slicesEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
