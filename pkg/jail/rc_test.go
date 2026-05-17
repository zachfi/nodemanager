package jail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/handler"
)

func TestEnsureJailRCService_MountsFstabBeforeJailC(t *testing.T) {
	rcServiceDir := t.TempDir()
	confDir := t.TempDir()
	fstabPath := filepath.Join(t.TempDir(), "web", "fstab")

	exec := &handler.MockExecHandler{Status: []int{1, 0}} // sysrc -n → "", sysrc =YES → ok

	require.NoError(t, ensureJailRCService(context.Background(), exec, rcServiceDir, "web", confDir, fstabPath))

	scriptPath := filepath.Join(rcServiceDir, "jail_web")
	data, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	script := string(data)

	require.True(t, strings.Contains(script, fstabPath),
		"rc.d script must reference the per-jail fstab path")
	require.True(t, strings.Contains(script, "mount -F"),
		"rc.d script must mount fstab entries with mount -F <path> -a")

	mountIdx := strings.Index(script, "/sbin/mount -F")
	jailIdx := strings.Index(script, "/usr/sbin/jail -c")
	require.NotEqual(t, -1, mountIdx, "mount call must be present")
	require.NotEqual(t, -1, jailIdx, "jail -c call must be present")
	require.Less(t, mountIdx, jailIdx,
		"fstab mount must run BEFORE jail -c to avoid FreeBSD VFS deadlock")
}

func TestEnsureJailRCService_GuardsMissingFstab(t *testing.T) {
	rcServiceDir := t.TempDir()
	confDir := t.TempDir()
	fstabPath := filepath.Join(t.TempDir(), "web", "fstab")

	exec := &handler.MockExecHandler{Status: []int{1, 0}}

	require.NoError(t, ensureJailRCService(context.Background(), exec, rcServiceDir, "web", confDir, fstabPath))

	data, err := os.ReadFile(filepath.Join(rcServiceDir, "jail_web"))
	require.NoError(t, err)
	require.True(t, strings.Contains(string(data), "[ -s \""+fstabPath+"\" ]"),
		"rc.d script must guard the mount call with a [ -s <path> ] test so an absent or empty fstab is a no-op")
}
