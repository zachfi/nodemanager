package jail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/zfs"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockReleaseManager satisfies ReleaseManager without touching the network or
// filesystem.
type mockReleaseManager struct {
	ensured  []string
	basePath string
}

func (m *mockReleaseManager) Ensure(_ context.Context, version string) error {
	m.ensured = append(m.ensured, version)
	return nil
}

func (m *mockReleaseManager) Path(version string) string {
	return filepath.Join(m.basePath, version)
}

// newTestManager builds a manager with injectable dependencies and a temp dir
// for both basePath and confDir. The returned MockExecHandler's Status queue
// should be pre-loaded by the caller to cover every ZFS list/create call.
func newTestManager(t *testing.T, statuses []int) (*manager, *handler.MockExecHandler, *mockReleaseManager) {
	t.Helper()
	basePath := t.TempDir()
	exec := &handler.MockExecHandler{Status: statuses}
	releases := &mockReleaseManager{basePath: filepath.Join(basePath, ReleaseRootDir)}
	m := &manager{
		basePath: basePath,
		dataset:  "zroot/nodemanager",
		confDir:  t.TempDir(),
		zfs:      zfs.NewZfsManager(exec),
		exec:     exec,
		releases: releases,
	}
	return m, exec, releases
}

func testJail(name, release string) freebsdv1.Jail {
	return freebsdv1.Jail{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: freebsdv1.JailSpec{
			NodeName: "myhost",
			Release:  release,
		},
	}
}

// zfsCalls returns the recorded ZFS command arguments for easy assertion.
func zfsCalls(exec *handler.MockExecHandler) [][]string {
	return exec.Recorder["/sbin/zfs"]
}

func TestEnsureJail_NewJail(t *testing.T) {
	// Status sequence for ZFS operations in EnsureJail:
	//   Exists(jailDataset)     → 1 (not found) → Ensure creates it (status 0)
	//   Exists(jailRootDataset) → 1 (not found) → Snapshot (0) + Clone (0)
	statuses := []int{1, 0, 1, 0, 0}
	m, exec, releases := newTestManager(t, statuses)

	j := testJail("classic", "14.2-RELEASE")
	require.NoError(t, m.EnsureJail(context.Background(), j))

	// Release must have been requested.
	require.Equal(t, []string{"14.2-RELEASE"}, releases.ensured)

	calls := zfsCalls(exec)
	require.GreaterOrEqual(t, len(calls), 5)

	// Check jail container dataset creation.
	require.Equal(t, []string{"list", "zroot/nodemanager/jails/classic"}, calls[0])
	require.Equal(t, []string{"create", "zroot/nodemanager/jails/classic"}, calls[1])

	// Check snapshot and clone for jail root.
	require.Equal(t, []string{"list", "zroot/nodemanager/jails/classic/root"}, calls[2])
	require.Equal(t, []string{"snapshot", "zroot/nodemanager/releases/14.2-RELEASE@classic"}, calls[3])

	cloneArgs := calls[4]
	require.Equal(t, "clone", cloneArgs[0])
	require.Equal(t, "zroot/nodemanager/releases/14.2-RELEASE@classic", cloneArgs[len(cloneArgs)-2])
	require.Equal(t, "zroot/nodemanager/jails/classic/root", cloneArgs[len(cloneArgs)-1])
	// Mountpoint option must be set explicitly.
	require.True(t, containsArg(cloneArgs, "-o"), "clone must pass -o flag")
	require.True(t, containsArgStarting(cloneArgs, "mountpoint="), "clone must set mountpoint")

	// jail.conf must have been written.
	confPath := filepath.Join(m.confDir, "classic.conf")
	data, err := os.ReadFile(confPath)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(data), `classic {`))
	require.True(t, strings.Contains(string(data), `host.hostname = "classic";`))
}

func TestEnsureJail_AlreadyExists(t *testing.T) {
	// Both jailDataset and jailRootDataset already exist → no create/snapshot/clone.
	statuses := []int{0, 0}
	m, exec, releases := newTestManager(t, statuses)

	j := testJail("existing", "14.2-RELEASE")
	require.NoError(t, m.EnsureJail(context.Background(), j))

	require.Equal(t, []string{"14.2-RELEASE"}, releases.ensured)

	calls := zfsCalls(exec)
	// Only two Exists checks; no create, snapshot, or clone.
	require.Len(t, calls, 2)
	require.Equal(t, "list", calls[0][0])
	require.Equal(t, "list", calls[1][0])
}

func TestEnsureJail_WithMounts(t *testing.T) {
	statuses := []int{1, 0, 1, 0, 0}
	m, _, _ := newTestManager(t, statuses)

	j := testJail("web", "14.2-RELEASE")
	j.Spec.Mounts = []freebsdv1.JailMount{
		{HostPath: "/data/www", JailPath: "/var/www", ReadOnly: true},
	}

	require.NoError(t, m.EnsureJail(context.Background(), j))

	// fstab must have been written alongside the jail.
	fstabPath := filepath.Join(m.basePath, JailRootDir, "web", "fstab")
	data, err := os.ReadFile(fstabPath)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(data), "/data/www"))
	require.True(t, strings.Contains(string(data), "nullfs"))
	require.True(t, strings.Contains(string(data), "ro"))

	// jail.conf must reference the fstab.
	confData, err := os.ReadFile(filepath.Join(m.confDir, "web.conf"))
	require.NoError(t, err)
	require.True(t, strings.Contains(string(confData), "mount.fstab"))
}

func TestDeleteJail(t *testing.T) {
	// Exists(jailDataset) → 0 (found) → DestroyDatasetRecursive (0) → DestroyDataset snapshot (0)
	statuses := []int{0, 0, 0}
	m, exec, _ := newTestManager(t, statuses)

	// Pre-create conf and fstab files to verify they are removed.
	j := testJail("gone", "14.2-RELEASE")
	confPath := filepath.Join(m.confDir, "gone.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("gone {}"), 0o644))
	fstabPath := filepath.Join(m.basePath, JailRootDir, "gone", "fstab")
	require.NoError(t, os.MkdirAll(filepath.Dir(fstabPath), 0o755))
	require.NoError(t, os.WriteFile(fstabPath, []byte("# fstab"), 0o644))

	require.NoError(t, m.DeleteJail(context.Background(), j))

	// Config files must be gone.
	require.NoFileExists(t, confPath)
	require.NoFileExists(t, fstabPath)

	calls := zfsCalls(exec)
	require.GreaterOrEqual(t, len(calls), 3)

	// Exists check for the jail container.
	require.Equal(t, []string{"list", "zroot/nodemanager/jails/gone"}, calls[0])
	// Recursive destroy of the jail container and its children.
	require.Equal(t, []string{"destroy", "-r", "zroot/nodemanager/jails/gone"}, calls[1])
	// Snapshot cleanup on the release.
	require.Equal(t, []string{"destroy", "zroot/nodemanager/releases/14.2-RELEASE@gone"}, calls[2])
}

func TestStartStopRestartJail(t *testing.T) {
	cases := []struct {
		name    string
		op      func(context.Context, Manager, string) error
		wantCmd []string
	}{
		{
			name:    "start",
			op:      func(ctx context.Context, m Manager, n string) error { return m.StartJail(ctx, n) },
			wantCmd: []string{"jail", "start", "classic"},
		},
		{
			name:    "stop",
			op:      func(ctx context.Context, m Manager, n string) error { return m.StopJail(ctx, n) },
			wantCmd: []string{"jail", "stop", "classic"},
		},
		{
			name:    "restart",
			op:      func(ctx context.Context, m Manager, n string) error { return m.RestartJail(ctx, n) },
			wantCmd: []string{"jail", "restart", "classic"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, exec, _ := newTestManager(t, []int{0})
			require.NoError(t, tc.op(context.Background(), m, "classic"))
			// service is invoked via SimpleRunCommand → RunCommand
			require.Equal(t, tc.wantCmd, exec.Recorder["service"][0])
		})
	}
}

func TestIsRunning(t *testing.T) {
	running := `{"__version":"2","jail-information":{"jail":[{"jid":1,"hostname":"classic","path":"/jails/classic/root","name":"classic","state":"ACTIVE","cpusetid":1,"ipv4_addrs":[],"ipv6_addrs":[]}]}}`

	t.Run("running", func(t *testing.T) {
		m, _, _ := newTestManager(t, []int{0})
		m.exec.(*handler.MockExecHandler).Output = []string{running}

		ok, err := m.IsRunning(context.Background(), "classic")
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("not running", func(t *testing.T) {
		m, _, _ := newTestManager(t, []int{0})
		m.exec.(*handler.MockExecHandler).Output = []string{`{"__version":"2","jail-information":{"jail":[]}}`}

		ok, err := m.IsRunning(context.Background(), "classic")
		require.NoError(t, err)
		require.False(t, ok)
	})
}

func TestDeleteJailStopsFirst(t *testing.T) {
	// stop (status 0) + Exists(jailDataset) (0) + DestroyRecursive (0) + DestroySnapshot (0)
	statuses := []int{0, 0, 0, 0}
	m, exec, _ := newTestManager(t, statuses)

	j := testJail("gone", "14.2-RELEASE")
	// Pre-create conf/fstab so removal succeeds.
	confPath := filepath.Join(m.confDir, "gone.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("gone {}"), 0o644))
	fstabPath := filepath.Join(m.basePath, JailRootDir, "gone", "fstab")
	require.NoError(t, os.MkdirAll(filepath.Dir(fstabPath), 0o755))
	require.NoError(t, os.WriteFile(fstabPath, []byte("# fstab"), 0o644))

	// Provide empty jls output for the IsRunning call inside StopJail? No —
	// StopJail does not check IsRunning first; it just runs the command.
	// Supply a jls output for the call if needed. But StopJail just calls
	// `service jail stop`; no jls call is made during stop.
	require.NoError(t, m.DeleteJail(context.Background(), j))

	// First call to the "service" binary must be the stop command.
	// The mock records args only (not the binary name itself).
	require.Equal(t, []string{"jail", "stop", "gone"}, exec.Recorder["service"][0])
}

// helpers

func containsArg(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}

func containsArgStarting(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}
