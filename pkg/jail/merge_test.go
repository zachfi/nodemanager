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
		{
			name: "pf — template provides base rules, jail extends",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				PF: &freebsdv1.JailPF{
					Rules: []string{"pass in proto tcp to port 80"},
				},
			},
			tmpl: freebsdv1.JailTemplateSpec{
				PF: &freebsdv1.JailPF{
					Rules: []string{"block all"},
				},
			},
			want: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				PF: &freebsdv1.JailPF{
					Rules: []string{"block all", "pass in proto tcp to port 80"},
				},
			},
		},
		{
			name: "pf — only template rules, no jail pf",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
			},
			tmpl: freebsdv1.JailTemplateSpec{
				PF: &freebsdv1.JailPF{
					AnchorName: "jails/template",
					Rules:      []string{"block all"},
				},
			},
			want: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				PF: &freebsdv1.JailPF{
					AnchorName: "jails/template",
					Rules:      []string{"block all"},
				},
			},
		},
		{
			name: "pf — jail anchor name overrides template anchor name",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				PF: &freebsdv1.JailPF{
					AnchorName: "custom/web01",
					Rules:      []string{"pass in proto tcp to port 443"},
				},
			},
			tmpl: freebsdv1.JailTemplateSpec{
				PF: &freebsdv1.JailPF{
					AnchorName: "jails/template",
					Rules:      []string{"block all"},
				},
			},
			want: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				PF: &freebsdv1.JailPF{
					AnchorName: "custom/web01",
					Rules:      []string{"block all", "pass in proto tcp to port 443"},
				},
			},
		},
		{
			name: "pf — nil on both sides produces nil",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
			},
			tmpl: freebsdv1.JailTemplateSpec{},
			want: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
			},
		},
		{
			name: "parameters — template fills missing keys",
			spec: freebsdv1.JailSpec{
				NodeName:   "host01",
				Release:    "14.2-RELEASE",
				Parameters: map[string]string{"children.max": "5"},
			},
			tmpl: freebsdv1.JailTemplateSpec{
				Parameters: map[string]string{
					"allow.mount.zfs":   "",
					"allow.mount.tmpfs": "",
					"enforce_statfs":    "1",
				},
			},
			want: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
				Parameters: map[string]string{
					"children.max":      "5",
					"allow.mount.zfs":   "",
					"allow.mount.tmpfs": "",
					"enforce_statfs":    "1",
				},
			},
		},
		{
			name: "parameters — jail overrides template key",
			spec: freebsdv1.JailSpec{
				NodeName:   "host01",
				Release:    "14.2-RELEASE",
				Parameters: map[string]string{"enforce_statfs": "2"},
			},
			tmpl: freebsdv1.JailTemplateSpec{
				Parameters: map[string]string{"enforce_statfs": "1"},
			},
			want: freebsdv1.JailSpec{
				NodeName:   "host01",
				Release:    "14.2-RELEASE",
				Parameters: map[string]string{"enforce_statfs": "2"},
			},
		},
		{
			name: "parameters — nil template leaves spec unchanged",
			spec: freebsdv1.JailSpec{
				NodeName:   "host01",
				Release:    "14.2-RELEASE",
				Parameters: map[string]string{"children.max": "10"},
			},
			tmpl: freebsdv1.JailTemplateSpec{},
			want: freebsdv1.JailSpec{
				NodeName:   "host01",
				Release:    "14.2-RELEASE",
				Parameters: map[string]string{"children.max": "10"},
			},
		},
		{
			name: "parameters — nil spec inherits template",
			spec: freebsdv1.JailSpec{
				NodeName: "host01",
				Release:  "14.2-RELEASE",
			},
			tmpl: freebsdv1.JailTemplateSpec{
				Parameters: map[string]string{"allow.mount.zfs": ""},
			},
			want: freebsdv1.JailSpec{
				NodeName:   "host01",
				Release:    "14.2-RELEASE",
				Parameters: map[string]string{"allow.mount.zfs": ""},
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
