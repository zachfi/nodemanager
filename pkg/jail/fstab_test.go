package jail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

func TestWriteFstab(t *testing.T) {
	cases := []struct {
		name     string
		jailRoot string
		mounts   []freebsdv1.JailMount
		want     []string
	}{
		{
			name:     "single nullfs ro mount",
			jailRoot: "/usr/local/nodemanager/jails/web/root",
			mounts: []freebsdv1.JailMount{
				{HostPath: "/data/www", JailPath: "/var/www", ReadOnly: true},
			},
			want: []string{
				"/data/www\t/usr/local/nodemanager/jails/web/root/var/www\tnullfs\tro\t0\t0",
			},
		},
		{
			name:     "rw mount with explicit type",
			jailRoot: "/usr/local/nodemanager/jails/db/root",
			mounts: []freebsdv1.JailMount{
				{HostPath: "/srv/db", JailPath: "/var/db/postgres", Type: "nullfs"},
			},
			want: []string{
				"/srv/db\t/usr/local/nodemanager/jails/db/root/var/db/postgres\tnullfs\trw\t0\t0",
			},
		},
		{
			name:     "multiple mounts",
			jailRoot: "/usr/local/nodemanager/jails/app/root",
			mounts: []freebsdv1.JailMount{
				{HostPath: "/data/shared", JailPath: "/shared", ReadOnly: true},
				{HostPath: "/srv/logs", JailPath: "/var/log/app"},
			},
			want: []string{
				"/data/shared\t/usr/local/nodemanager/jails/app/root/shared\tnullfs\tro\t0\t0",
				"/srv/logs\t/usr/local/nodemanager/jails/app/root/var/log/app\tnullfs\trw\t0\t0",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fstabPath := filepath.Join(dir, "fstab")

			err := writeFstab(fstabPath, tc.jailRoot, tc.mounts)
			require.NoError(t, err)

			data, err := os.ReadFile(fstabPath)
			require.NoError(t, err)
			content := string(data)

			for _, want := range tc.want {
				require.True(t, strings.Contains(content, want),
					"expected line %q in fstab:\n%s", want, content)
			}
		})
	}
}

func TestRemoveFstab(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fstab")

	mounts := []freebsdv1.JailMount{{HostPath: "/data", JailPath: "/mnt/data"}}
	require.NoError(t, writeFstab(path, "/jail/root", mounts))
	require.FileExists(t, path)

	require.NoError(t, removeFstab(path))
	require.NoFileExists(t, path)

	// Removing a non-existent fstab should not error.
	require.NoError(t, removeFstab(path))
}
