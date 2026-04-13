package jail

import (
	"testing"

	"github.com/stretchr/testify/require"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

func TestMergeTemplateDefaults(t *testing.T) {
	cases := []struct {
		name string
		spec freebsdv1.JailSpec
		tmpl freebsdv1.JailTemplateSpec
		want freebsdv1.JailSpec
	}{
		{
			name: "template fills empty fields",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
			},
			tmpl: freebsdv1.JailTemplateSpec{
				Interface: "lo1",
				Mounts: []freebsdv1.JailMount{
					{HostPath: "/data", JailPath: "/mnt/data"},
				},
				Update: freebsdv1.JailUpdate{
					Schedule: "0 3 * * *",
					Delay:    "24h",
					Group:    "jails",
				},
			},
			want: freebsdv1.JailSpec{
				NodeName:  "host01",
				Release:   "14.2-RELEASE",
				Interface: "lo1",
				Mounts: []freebsdv1.JailMount{
					{HostPath: "/data", JailPath: "/mnt/data"},
				},
				Update: freebsdv1.JailUpdate{
					Schedule: "0 3 * * *",
					Delay:    "24h",
					Group:    "jails",
				},
			},
		},
		{
			name: "spec values override template",
			spec: freebsdv1.JailSpec{
				NodeName:  "host01",
				Release:   "14.2-RELEASE",
				Interface: "em0",
				Mounts: []freebsdv1.JailMount{
					{HostPath: "/custom", JailPath: "/mnt/custom"},
				},
				Update: freebsdv1.JailUpdate{
					Schedule: "0 5 * * *",
					Delay:    "48h",
					Group:    "special",
				},
			},
			tmpl: freebsdv1.JailTemplateSpec{
				Interface: "lo1",
				Mounts: []freebsdv1.JailMount{
					{HostPath: "/data", JailPath: "/mnt/data"},
				},
				Update: freebsdv1.JailUpdate{
					Schedule: "0 3 * * *",
					Delay:    "24h",
					Group:    "jails",
				},
			},
			want: freebsdv1.JailSpec{
				NodeName:  "host01",
				Release:   "14.2-RELEASE",
				Interface: "em0",
				Mounts: []freebsdv1.JailMount{
					{HostPath: "/custom", JailPath: "/mnt/custom"},
				},
				Update: freebsdv1.JailUpdate{
					Schedule: "0 5 * * *",
					Delay:    "48h",
					Group:    "special",
				},
			},
		},
		{
			name: "partial update merge — spec schedule, template delay",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				Update: freebsdv1.JailUpdate{
					Schedule: "0 5 * * *",
				},
			},
			tmpl: freebsdv1.JailTemplateSpec{
				Interface: "lo1",
				Update: freebsdv1.JailUpdate{
					Schedule: "0 3 * * *",
					Delay:    "24h",
					Group:    "jails",
				},
			},
			want: freebsdv1.JailSpec{
				NodeName:  "host01",
				Release:   "14.2-RELEASE",
				Interface: "lo1",
				Update: freebsdv1.JailUpdate{
					Schedule: "0 5 * * *",
					Delay:    "24h",
					Group:    "jails",
				},
			},
		},
		{
			name: "empty template — spec unchanged",
			spec: freebsdv1.JailSpec{
				NodeName:  "host01",
				Release:   "14.2-RELEASE",
				Interface: "em0",
				Inet:      "10.0.0.5",
			},
			tmpl: freebsdv1.JailTemplateSpec{},
			want: freebsdv1.JailSpec{
				NodeName:  "host01",
				Release:   "14.2-RELEASE",
				Interface: "em0",
				Inet:      "10.0.0.5",
			},
		},
		{
			name: "per-jail identity fields preserved",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				Hostname: "web01.internal",
				Inet:     "10.0.0.5",
				Inet6:    "2001:db8::5",
			},
			tmpl: freebsdv1.JailTemplateSpec{
				Interface: "lo1",
			},
			want: freebsdv1.JailSpec{
				NodeName:  "host01",
				Release:   "14.2-RELEASE",
				Hostname:  "web01.internal",
				Interface: "lo1",
				Inet:      "10.0.0.5",
				Inet6:     "2001:db8::5",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeTemplateDefaults(tc.spec, tc.tmpl)
			require.Equal(t, tc.want, got)
		})
	}
}
