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
		name      string
		jailName  string
		jailRoot  string
		fstabPath string
		spec      freebsdv1.JailSpec
		want      []string
		notWant   []string
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
				Inet:      "192.168.1.10",
			},
			want: []string{
				`interface = "em0";`,
				`ip4.addr = 192.168.1.10;`,
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
				Inet:      "10.0.0.5",
				Inet6:     "2001:db8::5",
			},
			want: []string{
				`interface = "vtnet0";`,
				`ip4.addr = 10.0.0.5;`,
				`ip6.addr = 2001:db8::5;`,
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
					"children.max":    "10",
					"enforce_statfs":  "1",
					"allow.mount.zfs": "",
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
			name:      "with fstab",
			jailName:  "storage",
			jailRoot:  "/usr/local/nodemanager/jails/storage/root",
			fstabPath: "/usr/local/nodemanager/jails/storage/fstab",
			spec: freebsdv1.JailSpec{
				Release: "14.2-RELEASE",
				Mounts:  []freebsdv1.JailMount{{HostPath: "/data", JailPath: "/mnt/data"}},
			},
			want: []string{
				`mount.fstab = "/usr/local/nodemanager/jails/storage/fstab";`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			err := writeJailConf(dir, tc.jailName, tc.jailRoot, tc.fstabPath, tc.spec)
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

func TestRemoveJailConf(t *testing.T) {
	dir := t.TempDir()
	name := "todelete"

	// Write a conf file then remove it.
	require.NoError(t, writeJailConf(dir, name, "/jail/root", "", freebsdv1.JailSpec{Release: "14.2-RELEASE"}))
	require.FileExists(t, filepath.Join(dir, name+".conf"))

	require.NoError(t, removeJailConf(dir, name))
	require.NoFileExists(t, filepath.Join(dir, name+".conf"))

	// Removing a non-existent file should not error.
	require.NoError(t, removeJailConf(dir, name))
}
