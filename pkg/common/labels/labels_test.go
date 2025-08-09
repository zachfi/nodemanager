package labels

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelGate(t *testing.T) {
	cases := []struct {
		name   string
		source map[string]string
		dest   map[string]string
		b      bool
		l      Logic
	}{
		{
			name:   "ones and",
			source: map[string]string{"one": "1"},
			dest:   map[string]string{"one": "1"},
			b:      true,
			l:      And,
		},
		{
			name:   "ones of",
			source: map[string]string{"one": "1"},
			dest:   map[string]string{"one": "1"},
			b:      true,
			l:      Or,
		},
		{
			name: "one is not 2",
			source: map[string]string{
				"one": "2",
			},
			dest: map[string]string{
				"one": "1",
			},
			b: false,
			l: Or,
		},
		{
			name:   "one is not 2 and",
			source: map[string]string{"one": "2"},
			dest:   map[string]string{"one": "1"},
			b:      false,
			l:      And,
		},
		{
			name: "many source one dest or ",
			source: map[string]string{
				"one":   "1",
				"two":   "2",
				"three": "3",
			},
			dest: map[string]string{
				"three": "3",
			},
			b: true,
			l: Or,
		},
		{
			name: "many source one dest and",
			source: map[string]string{
				"one":   "1",
				"two":   "2",
				"three": "3",
			},
			dest: map[string]string{
				"three": "3",
			},
			b: false,
			l: And,
		},
		{
			name: "key match no value",
			source: map[string]string{
				"one":   "1",
				"two":   "1",
				"three": "1",
			},
			dest: map[string]string{
				"three": "3",
			},
			b: true,
			l: AnyKey,
		},
		{
			name: "no matches",
			source: map[string]string{
				"one":   "1",
				"two":   "1",
				"three": "1",
			},
			dest: map[string]string{
				"three": "3",
			},
			b: true,
			l: NoneMatch,
		},
		{
			name: "a match is found when it shouldn't be",
			source: map[string]string{
				"one":   "1",
				"two":   "1",
				"three": "1",
			},
			dest: map[string]string{
				"three": "1",
			},
			b: false,
			l: NoneMatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := LabelGate(tc.l, tc.source, tc.dest)
			require.Equal(t, tc.b, b)
		})
	}
}
