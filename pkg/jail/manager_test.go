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
	//   Exists(jailRootDataset) → 1 (not found)
	//   Exists(snapshot)        → 1 (not found) → Snapshot (0) + Clone (0)
	statuses := []int{1, 0, 1, 1, 0, 0}
	m, exec, releases := newTestManager(t, statuses)

	j := testJail("classic", "14.2-RELEASE")
	require.NoError(t, m.EnsureJail(context.Background(), j))

	// Release must have been requested.
	require.Equal(t, []string{"14.2-RELEASE"}, releases.ensured)

	calls := zfsCalls(exec)
	require.GreaterOrEqual(t, len(calls), 6)

	// Check jail container dataset creation.
	require.Equal(t, []string{"list", "zroot/nodemanager/jails/classic"}, calls[0])
	require.Equal(t, []string{"create", "zroot/nodemanager/jails/classic"}, calls[1])

	// Check snapshot existence probe, then snapshot and clone for jail root.
	require.Equal(t, []string{"list", "zroot/nodemanager/jails/classic/root"}, calls[2])
	require.Equal(t, []string{"list", "zroot/nodemanager/releases/14.2-RELEASE@classic"}, calls[3])
	require.Equal(t, []string{"snapshot", "zroot/nodemanager/releases/14.2-RELEASE@classic"}, calls[4])

	cloneArgs := calls[5]
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
	// Both jailDataset and jailRootDataset already exist, and origin matches
	// spec.release → no create/snapshot/clone.
	statuses := []int{0, 0, 0} // Exists(jail), Exists(root), GetProperty(origin)
	m, exec, releases := newTestManager(t, statuses)
	// Output is consumed in call order; pad the two Exists calls with empty
	// strings, then provide the real origin for GetProperty.
	exec.Output = []string{"", "", "zroot/nodemanager/releases/14.2-RELEASE@existing"}

	j := testJail("existing", "14.2-RELEASE")
	require.NoError(t, m.EnsureJail(context.Background(), j))

	require.Equal(t, []string{"14.2-RELEASE"}, releases.ensured)

	calls := zfsCalls(exec)
	// Two Exists checks + one GetProperty; no create, snapshot, or clone.
	require.Len(t, calls, 3)
	require.Equal(t, "list", calls[0][0])
	require.Equal(t, "list", calls[1][0])
	require.Equal(t, []string{"get", "-H", "-o", "value", "origin", "zroot/nodemanager/jails/existing/root"}, calls[2])
}

func TestEnsureJail_ReleaseDrift(t *testing.T) {
	// Root exists but was cloned from 14.2-RELEASE; spec now wants 16.2-RELEASE.
	// Expected ZFS calls: Exists(jail)=found, Exists(root)=found, GetProperty=old origin,
	// stop (noop), DestroyRecursive(root), DestroyDataset(snapshot),
	// Exists(new snapshot)=not found, Snapshot, Clone.
	statuses := []int{0, 0, 0, 0, 0, 0, 1, 0, 0}
	m, exec, releases := newTestManager(t, statuses)
	// Pad empty outputs for the two Exists calls, then the old origin for GetProperty.
	exec.Output = []string{"", "", "zroot/nodemanager/releases/14.2-RELEASE@web01"}

	j := testJail("web01", "16.2-RELEASE")
	require.NoError(t, m.EnsureJail(context.Background(), j))

	require.Equal(t, []string{"16.2-RELEASE"}, releases.ensured)

	calls := zfsCalls(exec)
	// Verify destroy was called on the stale root.
	ops := make([]string, len(calls))
	for i, c := range calls {
		ops[i] = c[0]
	}
	require.Contains(t, ops, "destroy")
	// Verify a new snapshot and clone were created for 16.2-RELEASE.
	require.Contains(t, ops, "snapshot")
	require.Contains(t, ops, "clone")
}

func TestEnsureJail_WithMounts(t *testing.T) {
	statuses := []int{1, 0, 1, 1, 0, 0}
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

func TestInstalledRelease(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))

	t.Run("parses USERLAND_VERSION", func(t *testing.T) {
		script := "#!/bin/sh\nUSERLAND_VERSION=\"14.2-RELEASE\"\nexport USERLAND_VERSION\n"
		require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "freebsd-version"), []byte(script), 0o755))
		v, err := installedRelease(root)
		require.NoError(t, err)
		require.Equal(t, "14.2-RELEASE", v)
	})

	t.Run("error when file missing", func(t *testing.T) {
		_, err := installedRelease(t.TempDir())
		require.Error(t, err)
	})
}

func TestDeleteJail(t *testing.T) {
	// All commands default to status 0; statuses queue covers the first three
	// (jail -r, pfctl, mount -p) so the rest draw from the empty queue (→ 0).
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
	require.GreaterOrEqual(t, len(calls), 5)

	// Force-unmount of the jail root ZFS dataset before destroy.
	require.Equal(t, []string{"umount", "-f", "zroot/nodemanager/jails/gone/root"}, calls[0])
	// Existence check then recursive force-destroy of the jail container and its children.
	require.Equal(t, []string{"list", "zroot/nodemanager/jails/gone"}, calls[1])
	require.Equal(t, []string{"destroy", "-r", "-f", "zroot/nodemanager/jails/gone"}, calls[2])
	// Existence check then snapshot cleanup on the release.
	require.Equal(t, []string{"list", "zroot/nodemanager/releases/14.2-RELEASE@gone"}, calls[3])
	require.Equal(t, []string{"destroy", "zroot/nodemanager/releases/14.2-RELEASE@gone"}, calls[4])
}

func TestStartStopRestartJail(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		m, exec, _ := newTestManager(t, []int{0})
		require.NoError(t, m.StartJail(context.Background(), "classic"))
		confPath := filepath.Join(m.confDir, "classic.conf")
		require.Equal(t, []string{"-c", "-f", confPath, "classic"}, exec.Recorder["jail"][0])
	})

	t.Run("stop", func(t *testing.T) {
		m, exec, _ := newTestManager(t, []int{0})
		require.NoError(t, m.StopJail(context.Background(), "classic"))
		require.Equal(t, []string{"-r", "classic"}, exec.Recorder["jail"][0])
	})

	t.Run("restart", func(t *testing.T) {
		// Restart calls stop then start, so two jail invocations.
		m, exec, _ := newTestManager(t, []int{0, 0})
		require.NoError(t, m.RestartJail(context.Background(), "classic"))
		require.Len(t, exec.Recorder["jail"], 2)
		require.Equal(t, []string{"-r", "classic"}, exec.Recorder["jail"][0])
		confPath := filepath.Join(m.confDir, "classic.conf")
		require.Equal(t, []string{"-c", "-f", confPath, "classic"}, exec.Recorder["jail"][1])
	})
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

	// StopJail does not check IsRunning first; it just runs `jail -r`.
	require.NoError(t, m.DeleteJail(context.Background(), j))

	// First call to "jail" binary must be the stop command.
	// The mock records args only (not the binary name itself).
	require.Equal(t, []string{"-r", "gone"}, exec.Recorder["jail"][0])
}

func TestEnsureJail_WithPF(t *testing.T) {
	statuses := []int{1, 0, 1, 1, 0, 0, 0} // ZFS ops (with snapshot exists check) + pfctl success
	m, exec, _ := newTestManager(t, statuses)

	j := testJail("web", "14.2-RELEASE")
	j.Spec.PF = &freebsdv1.JailPF{
		Rules: []string{"block all", "pass in proto tcp to port 80"},
	}

	require.NoError(t, m.EnsureJail(context.Background(), j))

	// pfctl must have been called with the anchor and stdin flag.
	pfctlArgs := exec.Recorder["pfctl"]
	require.Len(t, pfctlArgs, 1)
	require.Equal(t, []string{"-a", "jails/web", "-f", "-"}, pfctlArgs[0])

	// The rules must have been passed via stdin.
	pfctlInputs := exec.InputRecorder["pfctl"]
	require.Len(t, pfctlInputs, 1)
	require.Contains(t, pfctlInputs[0], "block all")
	require.Contains(t, pfctlInputs[0], "pass in proto tcp to port 80")
}

func TestEnsureJail_NoPF(t *testing.T) {
	statuses := []int{1, 0, 1, 1, 0, 0}
	m, exec, _ := newTestManager(t, statuses)

	j := testJail("plain", "14.2-RELEASE")
	require.NoError(t, m.EnsureJail(context.Background(), j))

	// pfctl must not have been called when PF is not configured.
	require.Empty(t, exec.Recorder["pfctl"])
}

func TestDeleteJail_FlushesAnchor(t *testing.T) {
	// stop (0) + pfctl flush (0) + Exists(jailDataset) (0) + DestroyRecursive (0) + DestroySnapshot (0)
	statuses := []int{0, 0, 0, 0, 0}
	m, exec, _ := newTestManager(t, statuses)

	j := testJail("gone", "14.2-RELEASE")
	confPath := filepath.Join(m.confDir, "gone.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("gone {}"), 0o644))
	fstabPath := filepath.Join(m.basePath, JailRootDir, "gone", "fstab")
	require.NoError(t, os.MkdirAll(filepath.Dir(fstabPath), 0o755))
	require.NoError(t, os.WriteFile(fstabPath, []byte("# fstab"), 0o644))

	require.NoError(t, m.DeleteJail(context.Background(), j))

	// pfctl -F all must have been called for the default anchor.
	pfctlArgs := exec.Recorder["pfctl"]
	require.Len(t, pfctlArgs, 1)
	require.Equal(t, []string{"-a", "jails/gone", "-F", "all"}, pfctlArgs[0])
}

func TestJailAnchorName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		require.Equal(t, "jails/web01", jailAnchorName("web01", nil))
	})
	t.Run("default from empty spec", func(t *testing.T) {
		require.Equal(t, "jails/web01", jailAnchorName("web01", &freebsdv1.JailPF{}))
	})
	t.Run("custom anchor name", func(t *testing.T) {
		require.Equal(t, "custom/web01", jailAnchorName("web01", &freebsdv1.JailPF{AnchorName: "custom/web01"}))
	})
}

func TestDeleteJail_RemovesIPAliases(t *testing.T) {
	// When spec has Interface + Inet/Inet6, DeleteJail must explicitly remove
	// the IP aliases via ifconfig in case jail -r failed to clean them up.
	m, exec, _ := newTestManager(t, []int{0, 0, 0})

	j := testJail("gone", "14.2-RELEASE")
	j.Spec.Interface = "lo0"
	j.Spec.Inet = "10.0.1.5/24"
	j.Spec.Inet6 = "fd00::1/64"

	confPath := filepath.Join(m.confDir, "gone.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("gone {}"), 0o644))
	fstabPath := filepath.Join(m.basePath, JailRootDir, "gone", "fstab")
	require.NoError(t, os.MkdirAll(filepath.Dir(fstabPath), 0o755))
	require.NoError(t, os.WriteFile(fstabPath, []byte("# fstab"), 0o644))

	require.NoError(t, m.DeleteJail(context.Background(), j))

	// CIDR suffix must be stripped; both inet and inet6 must be removed.
	ifcfgArgs := exec.Recorder["ifconfig"]
	require.Len(t, ifcfgArgs, 2)
	require.Equal(t, []string{"lo0", "inet", "10.0.1.5", "-alias"}, ifcfgArgs[0])
	require.Equal(t, []string{"lo0", "inet6", "fd00::1", "-alias"}, ifcfgArgs[1])
}

func TestEnsureJail_NetworkChangeCyclesJail(t *testing.T) {
	// Jail is running with an old IP; spec has a new one.  EnsureJail must
	// stop the jail so the controller will restart it with the updated conf.
	//
	// jls output shows the jail running with 10.0.0.5; spec says 10.0.0.6.
	jlsOut := `{"__version":"2","jail-information":{"jail":[` +
		`{"jid":3,"hostname":"netjail","path":"/p","name":"netjail","state":"ACTIVE",` +
		`"cpusetid":1,"ipv4_addrs":["10.0.0.5"],"ipv6_addrs":[]}` +
		`]}}`

	// Status sequence: Exists(jail)=found, Exists(root)=found, GetProperty=match.
	// jls and StopJail draw from the empty queue (→ 0, success).
	m, exec, _ := newTestManager(t, []int{0, 0, 0})
	exec.Output = []string{"", "", "zroot/nodemanager/releases/14.2-RELEASE@netjail", jlsOut}

	j := testJail("netjail", "14.2-RELEASE")
	j.Spec.Interface = "lo0"
	j.Spec.Inet = "10.0.0.6"

	require.NoError(t, m.EnsureJail(context.Background(), j))

	// jail -r must have been called to cycle the jail.
	require.Equal(t, []string{"-r", "netjail"}, exec.Recorder["jail"][0])
}

func TestEnsureJail_OrphanedIPAliasCleanedBeforeStart(t *testing.T) {
	// Jail is NOT running (e.g. crashed) but its IP alias is still on the
	// interface.  EnsureJail must remove the alias so jail -c does not fail
	// with "ifconfig: ioctl (SIOCAIFADDR): File exists".
	//
	// jls returns no jails — nothing running.
	jlsOut := `{"__version":"2","jail-information":{"jail":[]}}`

	m, exec, _ := newTestManager(t, []int{0, 0, 0})
	exec.Output = []string{"", "", "zroot/nodemanager/releases/14.2-RELEASE@gone", jlsOut}

	j := testJail("gone", "14.2-RELEASE")
	j.Spec.Interface = "lo1"
	j.Spec.Inet6 = "fc20::301/128"

	require.NoError(t, m.EnsureJail(context.Background(), j))

	// ifconfig must have been called to clear the (potentially orphaned) alias.
	found := false
	for _, args := range exec.Recorder["ifconfig"] {
		if containsArg(args, "fc20::301") && containsArg(args, "-alias") {
			found = true
		}
	}
	require.True(t, found, "orphaned inet6 alias must be removed before jail -c")

	// jail -r must NOT have been called (jail was not running).
	require.Empty(t, exec.Recorder["jail"])
}

func TestEnsureJail_NetworkUnchangedNoRestart(t *testing.T) {
	// Running jail already has the spec IP — no restart should be triggered.
	jlsOut := `{"__version":"2","jail-information":{"jail":[` +
		`{"jid":4,"hostname":"stable","path":"/p","name":"stable","state":"ACTIVE",` +
		`"cpusetid":1,"ipv4_addrs":["10.0.0.5"],"ipv6_addrs":[]}` +
		`]}}`

	m, exec, _ := newTestManager(t, []int{0, 0, 0})
	exec.Output = []string{"", "", "zroot/nodemanager/releases/14.2-RELEASE@stable", jlsOut}

	j := testJail("stable", "14.2-RELEASE")
	j.Spec.Interface = "lo0"
	j.Spec.Inet = "10.0.0.5" // same as running

	require.NoError(t, m.EnsureJail(context.Background(), j))

	// jail -r must NOT have been called.
	require.Empty(t, exec.Recorder["jail"])
}

func TestDeleteJail_ExplicitDevfsUnmount(t *testing.T) {
	// devfs at <jailRoot>/dev must always be explicitly umounted, independent
	// of what mount -p reports.
	m, exec, _ := newTestManager(t, []int{0, 0, 0})

	j := testJail("gone", "14.2-RELEASE")
	confPath := filepath.Join(m.confDir, "gone.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("gone {}"), 0o644))
	fstabPath := filepath.Join(m.basePath, JailRootDir, "gone", "fstab")
	require.NoError(t, os.MkdirAll(filepath.Dir(fstabPath), 0o755))
	require.NoError(t, os.WriteFile(fstabPath, []byte("# fstab"), 0o644))

	require.NoError(t, m.DeleteJail(context.Background(), j))

	devfsPath := filepath.Join(m.basePath, JailRootDir, "gone", "root", "dev")
	found := false
	for _, args := range exec.Recorder["umount"] {
		if containsArg(args, devfsPath) {
			found = true
		}
	}
	require.True(t, found, "devfs at <jailRoot>/dev must be explicitly unmounted")
}

func TestDeleteJail_WithMounts_ExplicitNullfsUnmount(t *testing.T) {
	// nullfs mounts declared in spec must be explicitly umounted by path.
	m, exec, _ := newTestManager(t, []int{0, 0, 0})

	j := testJail("gone", "14.2-RELEASE")
	j.Spec.Mounts = []freebsdv1.JailMount{
		{HostPath: "/data/www", JailPath: "/var/www", ReadOnly: true},
	}
	confPath := filepath.Join(m.confDir, "gone.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("gone {}"), 0o644))
	fstabPath := filepath.Join(m.basePath, JailRootDir, "gone", "fstab")
	require.NoError(t, os.MkdirAll(filepath.Dir(fstabPath), 0o755))
	require.NoError(t, os.WriteFile(fstabPath, []byte("# fstab"), 0o644))

	require.NoError(t, m.DeleteJail(context.Background(), j))

	nullfsPath := filepath.Join(m.basePath, JailRootDir, "gone", "root", "var", "www")
	found := false
	for _, args := range exec.Recorder["umount"] {
		if containsArg(args, nullfsPath) {
			found = true
		}
	}
	require.True(t, found, "nullfs mount at spec JailPath must be explicitly unmounted")
}

func TestEnsureJail_OrphanedSnapshot(t *testing.T) {
	// Snapshot exists (orphaned from a previous partial delete) but the jail
	// root dataset does not.  EnsureJail must reuse the snapshot rather than
	// failing with "snapshot already exists".
	//
	// Status sequence:
	//   Exists(jailDataset)     → 1 (not found) → create (0)
	//   Exists(jailRootDataset) → 1 (not found)
	//   Exists(snapshot)        → 0 (found! orphaned) → skip Snapshot, Clone (0)
	statuses := []int{1, 0, 1, 0, 0}
	m, exec, releases := newTestManager(t, statuses)

	j := testJail("dhcp1", "14.4-RELEASE")
	require.NoError(t, m.EnsureJail(context.Background(), j))

	require.Equal(t, []string{"14.4-RELEASE"}, releases.ensured)

	calls := zfsCalls(exec)
	ops := make([]string, len(calls))
	for i, c := range calls {
		ops[i] = c[0]
	}
	// Snapshot must NOT be re-created when it already exists.
	for _, op := range ops {
		require.NotEqual(t, "snapshot", op, "snapshot must not be re-created when it already exists")
	}
	// Clone must still be created from the existing snapshot.
	require.Contains(t, ops, "clone")
}

func TestDeleteJail_SnapshotAlreadyGone(t *testing.T) {
	// Snapshot was already cleaned up (e.g. manually) before this delete runs.
	// DeleteJail must succeed without attempting a second destroy.
	//
	// Status positions: jail(0), pfctl(1), umount-dev(2), mount-p(3),
	//   zfs-umount(4), Exists(jailDataset)=found(5), destroy-r-f(6),
	//   Exists(snapshot)=gone(7).
	statuses := []int{0, 0, 0, 0, 0, 0, 0, 1}
	m, exec, _ := newTestManager(t, statuses)

	j := testJail("gone", "14.2-RELEASE")
	confPath := filepath.Join(m.confDir, "gone.conf")
	require.NoError(t, os.WriteFile(confPath, []byte("gone {}"), 0o644))
	fstabPath := filepath.Join(m.basePath, JailRootDir, "gone", "fstab")
	require.NoError(t, os.MkdirAll(filepath.Dir(fstabPath), 0o755))
	require.NoError(t, os.WriteFile(fstabPath, []byte("# fstab"), 0o644))

	require.NoError(t, m.DeleteJail(context.Background(), j))

	// Only the recursive jail dataset destroy should appear; no snapshot destroy.
	calls := zfsCalls(exec)
	for _, c := range calls {
		if c[0] == "destroy" {
			require.Contains(t, c, "-r", "only recursive jail-dataset destroy expected; snapshot was already gone")
		}
	}
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
