package jail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

func TestWriteJailConf(t *testing.T) {
	cases := []struct {
		name     string
		jailName string
		jailRoot string
		spec     freebsdv1.JailSpec
		want     []string
		notWant  []string
	}{
		{
			name:     "minimal — no network, no fstab",
			jailName: "classic",
			jailRoot: "/usr/local/nodemanager/jails/classic/root",
			spec: freebsdv1.JailSpec{
				Release: "14.2-RELEASE",
			},
			want: []string{
				`classic {`,
				`host.hostname = "classic";`,
				`path          = "/usr/local/nodemanager/jails/classic/root";`,
				`exec.start = "/bin/sh /etc/rc";`,
				`exec.stop  = "/bin/sh /etc/rc.shutdown jail";`,
				`exec.consolelog = "/var/log/jail_classic_console.log";`,
				`mount.devfs;`,
				`enforce_statfs = 2;`,
				`securelevel = 2;`,
				`allow.raw_sockets;`,
				`osrelease = "14.2-RELEASE";`,
			},
			notWant: []string{
				"ip4.addr",
				"ip6.addr",
				"ip6 = new",
				"interface",
				"mount.fstab",
			},
		},
		{
			name:     "explicit hostname overrides resource name",
			jailName: "web01",
			jailRoot: "/usr/local/nodemanager/jails/web01/root",
			spec: freebsdv1.JailSpec{
				Release:  "14.2-RELEASE",
				Hostname: "web01.internal",
			},
			want: []string{
				`host.hostname = "web01.internal";`,
			},
		},
		{
			name:     "with IPv4 and interface",
			jailName: "db",
			jailRoot: "/usr/local/nodemanager/jails/db/root",
			spec: freebsdv1.JailSpec{
				Release:   "14.2-RELEASE",
				Interface: "em0",
				Inets:     []string{"192.0.2.10"},
			},
			want: []string{
				`interface = "em0";`,
				`ip4.addr = 192.0.2.10;`,
			},
			notWant: []string{"ip6.addr"},
		},
		{
			name:     "with dual-stack networking",
			jailName: "app",
			jailRoot: "/usr/local/nodemanager/jails/app/root",
			spec: freebsdv1.JailSpec{
				Release:   "14.2-RELEASE",
				Interface: "vtnet0",
				Inets:     []string{"192.0.2.5"},
				Inet6s:    []string{"2001:db8::5"},
			},
			want: []string{
				`interface = "vtnet0";`,
				`ip4.addr = 192.0.2.5;`,
				`ip6.addr = 2001:db8::5;`,
				`ip6 = new;`,
			},
		},
		{
			name:     "with multiple IPs per family (anycast)",
			jailName: "dns0",
			jailRoot: "/usr/local/nodemanager/jails/dns0/root",
			spec: freebsdv1.JailSpec{
				Release:   "14.2-RELEASE",
				Interface: "lo0",
				Inets:     []string{"192.0.2.105/27", "203.0.113.53/32"},
				Inet6s:    []string{"2001:db8::585/64", "2001:db8:ffff::53/128"},
			},
			want: []string{
				`ip4.addr = 192.0.2.105/27, 203.0.113.53/32;`,
				`ip6.addr = 2001:db8::585/64, 2001:db8:ffff::53/128;`,
				`ip6 = new;`,
			},
		},
		{
			name:     "with extra parameters — poudriere jail",
			jailName: "poud1",
			jailRoot: "/usr/local/nodemanager/jails/poud1/root",
			spec: freebsdv1.JailSpec{
				Release: "14.2-RELEASE",
				Parameters: map[string]string{
					"children.max":      "10",
					"enforce_statfs":    "1",
					"allow.mount.zfs":   "",
					"allow.mount.tmpfs": "",
				},
			},
			want: []string{
				`children.max = 10;`,
				`enforce_statfs = 1;`,
				`allow.mount.zfs;`,
				`allow.mount.tmpfs;`,
			},
		},
		{
			name:     "mounts use exec.prestart/poststop instead of mount.fstab",
			jailName: "storage",
			jailRoot: "/usr/local/nodemanager/jails/storage/root",
			spec: freebsdv1.JailSpec{
				Release: "14.2-RELEASE",
				Mounts:  []freebsdv1.JailMount{{HostPath: "/data", JailPath: "/mnt/data"}},
			},
			want: []string{
				`exec.prestart += "mount -t nullfs -o rw /data /usr/local/nodemanager/jails/storage/root/mnt/data";`,
				`exec.poststop += "umount -f /usr/local/nodemanager/jails/storage/root/mnt/data 2>/dev/null || true";`,
			},
			notWant: []string{
				"mount.fstab",
			},
		},
		{
			name:     "prestart: unmounts then mounts, deepest first for unmounts shallowest for mounts",
			jailName: "gar1",
			jailRoot: "/usr/local/nodemanager/jails/gar1/root",
			spec: freebsdv1.JailSpec{
				Release: "14.2-RELEASE",
				Mounts: []freebsdv1.JailMount{
					{HostPath: "/data01/garage1", JailPath: "/var/garage"},
					{HostPath: "/data01/meta1", JailPath: "/var/garage/meta"},
				},
			},
			want: []string{
				`exec.prestart += "while umount -f /usr/local/nodemanager/jails/gar1/root/dev 2>/dev/null; do true; done";`,
				`exec.prestart += "umount -f /usr/local/nodemanager/jails/gar1/root/var/garage 2>/dev/null || true";`,
				`exec.prestart += "umount -f /usr/local/nodemanager/jails/gar1/root/var/garage/meta 2>/dev/null || true";`,
				`exec.prestart += "mount -t nullfs -o rw /data01/garage1 /usr/local/nodemanager/jails/gar1/root/var/garage";`,
				`exec.prestart += "mount -t nullfs -o rw /data01/meta1 /usr/local/nodemanager/jails/gar1/root/var/garage/meta";`,
			},
		},
		{
			name:     "prestart devfs uses loop even with no extra mounts",
			jailName: "minimal",
			jailRoot: "/usr/local/nodemanager/jails/minimal/root",
			spec: freebsdv1.JailSpec{
				Release: "14.2-RELEASE",
			},
			want: []string{
				`exec.prestart += "while umount -f /usr/local/nodemanager/jails/minimal/root/dev 2>/dev/null; do true; done";`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := writeJailConf(dir, tc.jailName, tc.jailRoot, tc.spec)
			require.NoError(t, err)

			data, err := os.ReadFile(filepath.Join(dir, tc.jailName+".conf"))
			require.NoError(t, err)
			content := string(data)

			for _, want := range tc.want {
				require.True(t, strings.Contains(content, want),
					"expected %q in conf:\n%s", want, content)
			}
			for _, notWant := range tc.notWant {
				require.False(t, strings.Contains(content, notWant),
					"unexpected %q in conf:\n%s", notWant, content)
			}
		})
	}
}

func TestPrestartUnmounts(t *testing.T) {
	cases := []struct {
		name     string
		jailRoot string
		mounts   []freebsdv1.JailMount
		want     []string // expected umount target paths, in order (deepest first)
	}{
		{
			name:     "no extra mounts — devfs loop only",
			jailRoot: "/jail/root",
			want:     []string{"while umount -f /jail/root/dev"},
		},
		{
			name:     "single mount — deeper than devfs comes first",
			jailRoot: "/jail/root",
			mounts:   []freebsdv1.JailMount{{HostPath: "/data", JailPath: "/mnt/data"}},
			want:     []string{"/jail/root/mnt/data", "while umount -f /jail/root/dev"},
		},
		{
			name:     "nested mounts — deepest first, devfs uses loop",
			jailRoot: "/jail/root",
			mounts: []freebsdv1.JailMount{
				{HostPath: "/data/garage", JailPath: "/var/garage"},
				{HostPath: "/data/meta", JailPath: "/var/garage/meta"},
			},
			want: []string{"/jail/root/var/garage/meta", "/jail/root/var/garage", "while umount -f /jail/root/dev"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmds := prestartUnmounts(tc.jailRoot, tc.mounts)
			require.Len(t, cmds, len(tc.want))
			for i, wantPath := range tc.want {
				require.Contains(t, cmds[i], wantPath,
					"command %d should contain path %q", i, wantPath)
			}
		})
	}
}

func TestRemoveJailConf(t *testing.T) {
	dir := t.TempDir()
	name := "todelete"

	// Write a conf file then remove it.
	_, err := writeJailConf(dir, name, "/jail/root", freebsdv1.JailSpec{Release: "14.2-RELEASE"})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, name+".conf"))

	require.NoError(t, removeJailConf(dir, name))
	require.NoFileExists(t, filepath.Join(dir, name+".conf"))

	// Removing a non-existent file should not error.
	require.NoError(t, removeJailConf(dir, name))
}
