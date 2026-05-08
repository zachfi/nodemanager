package apply_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/apply"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/services"
)

var discardLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

// --- LocalResolver tests ---

func TestLocalResolver_CollectTemplateData(t *testing.T) {
	node := commonv1.ManagedNode{}
	node.Name = "myhost"
	node.Labels = map[string]string{"kubernetes.io/hostname": "myhost", "role": "server"}

	resolver := apply.NewLocalResolver(discardLogger, node, nil)
	ctx := context.Background()

	t.Run("populates node labels and status", func(t *testing.T) {
		file := commonv1.File{Path: "/etc/foo"}
		data, err := resolver.CollectTemplateData(ctx, "default", file, node)
		require.NoError(t, err)
		require.Equal(t, node.Labels, data.Node.Labels)
		require.Len(t, data.Nodes, 1)
		require.Equal(t, "myhost", data.Nodes[0].Name)
	})

	t.Run("no error when secretRefs present (warns instead)", func(t *testing.T) {
		file := commonv1.File{Path: "/etc/foo", SecretRefs: []string{"my-secret"}}
		_, err := resolver.CollectTemplateData(ctx, "default", file, node)
		require.NoError(t, err)
	})

	t.Run("no error when configMapRefs present (warns instead)", func(t *testing.T) {
		file := commonv1.File{Path: "/etc/foo", ConfigMapRefs: []string{"my-cm"}}
		_, err := resolver.CollectTemplateData(ctx, "default", file, node)
		require.NoError(t, err)
	})

	t.Run("secrets and configmaps are empty in offline mode", func(t *testing.T) {
		file := commonv1.File{
			Path:          "/etc/foo",
			SecretRefs:    []string{"s"},
			ConfigMapRefs: []string{"c"},
		}
		data, err := resolver.CollectTemplateData(ctx, "default", file, node)
		require.NoError(t, err)
		require.Empty(t, data.Node.Secrets)
		require.Empty(t, data.Node.ConfigMaps)
	})
}

func TestLocalResolver_ManagedPathsUnder(t *testing.T) {
	node := commonv1.ManagedNode{}
	node.Name = "myhost"
	node.Labels = map[string]string{"role": "server"}

	matchingCS := commonv1.ConfigSet{}
	matchingCS.Name = "matching"
	matchingCS.Labels = map[string]string{"role": "server"}
	matchingCS.Spec.Files = []commonv1.File{
		{Path: "/etc/app/a.conf"},
		{Path: "/etc/app/b.conf"},
		{Path: "/etc/other/c.conf"},
	}

	nonMatchingCS := commonv1.ConfigSet{}
	nonMatchingCS.Name = "nonmatching"
	nonMatchingCS.Labels = map[string]string{"role": "worker"}
	nonMatchingCS.Spec.Files = []commonv1.File{
		{Path: "/etc/app/worker.conf"},
	}

	resolver := apply.NewLocalResolver(discardLogger, node, []commonv1.ConfigSet{matchingCS, nonMatchingCS})
	ctx := context.Background()

	managed := resolver.ManagedPathsUnder(ctx, "default", node, "/etc/app")
	require.Contains(t, managed, "/etc/app/a.conf")
	require.Contains(t, managed, "/etc/app/b.conf")
	require.NotContains(t, managed, "/etc/other/c.conf", "path outside dir should be excluded")
	require.NotContains(t, managed, "/etc/app/worker.conf", "non-matching configset should be excluded")
}

func TestLocalResolver_ManagedPathsUnder_EmptyMatchers(t *testing.T) {
	node := commonv1.ManagedNode{}
	node.Name = "myhost"
	node.Labels = map[string]string{"role": "server"}

	// ConfigSet with no labels should not match any node.
	unlabelledCS := commonv1.ConfigSet{}
	unlabelledCS.Name = "unlabelled"
	unlabelledCS.Spec.Files = []commonv1.File{{Path: "/etc/app/x.conf"}}

	resolver := apply.NewLocalResolver(discardLogger, node, []commonv1.ConfigSet{unlabelledCS})
	managed := resolver.ManagedPathsUnder(context.Background(), "default", node, "/etc/app")
	require.Empty(t, managed)
}

// --- Applier.Files tests ---

func TestApplier_Files_InlineContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.conf")

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: target, Ensure: "file", Content: "hello world"},
	}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, target)
	require.Equal(t, "hello world", sys.fileHandler.writtenContent[target])
}

func TestApplier_Files_SourceFile(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "source.conf")
	require.NoError(t, os.WriteFile(srcFile, []byte("from source"), 0o644))

	target := filepath.Join(dir, "dest.conf")

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{ManifestRoot: dir})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: target, Ensure: "file", SourceFile: "source.conf"},
	}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, target)
	require.Equal(t, "from source", sys.fileHandler.writtenContent[target])
}

func TestApplier_Files_SourceFileMissing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dest.conf")

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{ManifestRoot: dir})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: target, Ensure: "file", SourceFile: "nonexistent.conf"},
	}

	_, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.Error(t, err)
}

func TestApplier_Files_AbsoluteSourceFile(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "abs.conf")
	require.NoError(t, os.WriteFile(srcFile, []byte("absolute"), 0o644))

	target := filepath.Join(dir, "dest.conf")

	sys := newMockSystem()
	// ManifestRoot set to something else — absolute SourceFile should ignore it.
	a := apply.New(sys, discardLogger, apply.Config{ManifestRoot: "/some/other/dir"})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: target, Ensure: "file", SourceFile: srcFile},
	}

	_, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Equal(t, "absolute", sys.fileHandler.writtenContent[target])
}

func TestApplier_Files_Absent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "old.conf")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: target, Ensure: "absent"},
	}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, target)
	require.Contains(t, sys.fileHandler.removedPaths, target)
}

func TestApplier_Files_CreateOnly_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.conf")
	require.NoError(t, os.WriteFile(target, []byte("original"), 0o644))

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: target, Ensure: "file", Content: "new content", CreateOnly: true},
	}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Empty(t, changed, "createOnly should not report a change when file exists")
	require.Empty(t, sys.fileHandler.writtenContent, "createOnly should not write when file exists")
}

func TestApplier_Files_CreateOnly_WritesNew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.conf")

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: target, Ensure: "file", Content: "seed content", CreateOnly: true},
	}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, target, "createOnly should write when file does not exist")
}

func TestApplier_Files_Directory(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, "newsubdir")

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{
		{Path: newDir, Ensure: "directory", Mode: "0755"},
	}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, newDir)
	info, statErr := os.Stat(newDir)
	require.NoError(t, statErr)
	require.True(t, info.IsDir())
}

// --- Applier.Files symlink tests ---

// TestApplier_Files_Symlink_CreatesWhenMissing verifies that a fresh path with
// no existing entry gets the desired symlink installed in one reconcile pass.
func TestApplier_Files_Symlink_CreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "resolv.conf")
	target := "/run/systemd/resolve/stub-resolv.conf"

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{{Path: link, Ensure: "symlink", Target: target}}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, link)

	got, readErr := os.Readlink(link)
	require.NoError(t, readErr)
	require.Equal(t, target, got)
}

// TestApplier_Files_Symlink_ReplacesRegularFile is the regression test for the
// /etc/resolv.conf case: when the path already holds a regular file (e.g. the
// default Arch resolv.conf) the reconciler must remove it and install the
// desired symlink. The previous implementation called os.Readlink first, which
// returned EINVAL on a regular file and aborted the reconcile so convergence
// was impossible.
func TestApplier_Files_Symlink_ReplacesRegularFile(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "resolv.conf")
	target := "/run/systemd/resolve/stub-resolv.conf"

	require.NoError(t, os.WriteFile(link, []byte("nameserver 1.1.1.1\n"), 0o644))

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{{Path: link, Ensure: "symlink", Target: target}}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, link)
	require.Contains(t, sys.fileHandler.removedPaths, link)

	got, readErr := os.Readlink(link)
	require.NoError(t, readErr)
	require.Equal(t, target, got)
}

// TestApplier_Files_Symlink_RetargetsExisting verifies that an existing
// symlink with the wrong target gets removed and replaced.
func TestApplier_Files_Symlink_RetargetsExisting(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "resolv.conf")
	wantTarget := "/run/systemd/resolve/stub-resolv.conf"

	require.NoError(t, os.Symlink("/etc/resolv.conf.head", link))

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{{Path: link, Ensure: "symlink", Target: wantTarget}}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.Contains(t, changed, link)

	got, readErr := os.Readlink(link)
	require.NoError(t, readErr)
	require.Equal(t, wantTarget, got)
}

// TestApplier_Files_Symlink_NoopWhenAlreadyCorrect verifies idempotence:
// a correct existing symlink results in no changes recorded and no remove.
func TestApplier_Files_Symlink_NoopWhenAlreadyCorrect(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "resolv.conf")
	target := "/run/systemd/resolve/stub-resolv.conf"

	require.NoError(t, os.Symlink(target, link))

	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})
	resolver := apply.NewLocalResolver(discardLogger, commonv1.ManagedNode{}, nil)

	fileSet := []commonv1.File{{Path: link, Ensure: "symlink", Target: target}}

	changed, _, err := a.Files(context.Background(), "host", "cs", "", fileSet, commonv1.ManagedNode{}, resolver)
	require.NoError(t, err)
	require.NotContains(t, changed, link)
	require.NotContains(t, sys.fileHandler.removedPaths, link)
}

// --- Applier.Packages tests ---

func TestApplier_Packages_InstallMissing(t *testing.T) {
	sys := newMockSystem()
	sys.packageHandler.installed = map[string]string{"existingpkg": "1.0"}

	a := apply.New(sys, discardLogger, apply.Config{})

	err := a.Packages(context.Background(), []commonv1.Package{
		{Name: "newpkg", Ensure: "installed"},
	})
	require.NoError(t, err)
	require.Contains(t, sys.packageHandler.installCalls, "newpkg")
}

func TestApplier_Packages_SkipAlreadyInstalled(t *testing.T) {
	sys := newMockSystem()
	sys.packageHandler.installed = map[string]string{"mypkg": "1.0"}

	a := apply.New(sys, discardLogger, apply.Config{})

	err := a.Packages(context.Background(), []commonv1.Package{
		{Name: "mypkg", Ensure: "installed"},
	})
	require.NoError(t, err)
	require.NotContains(t, sys.packageHandler.installCalls, "mypkg")
}

func TestApplier_Packages_VersionMismatchTriggersInstall(t *testing.T) {
	sys := newMockSystem()
	sys.packageHandler.installed = map[string]string{"mypkg": "1.0"}

	a := apply.New(sys, discardLogger, apply.Config{})

	err := a.Packages(context.Background(), []commonv1.Package{
		{Name: "mypkg", Ensure: "installed", Version: "2.0"},
	})
	require.NoError(t, err)
	require.Contains(t, sys.packageHandler.installCalls, "mypkg")
}

func TestApplier_Packages_RemovePresent(t *testing.T) {
	sys := newMockSystem()
	sys.packageHandler.installed = map[string]string{"unwanted": "1.0"}

	a := apply.New(sys, discardLogger, apply.Config{})

	err := a.Packages(context.Background(), []commonv1.Package{
		{Name: "unwanted", Ensure: "absent"},
	})
	require.NoError(t, err)
	require.Contains(t, sys.packageHandler.removeCalls, "unwanted")
}

func TestApplier_Packages_SkipAbsentAlreadyGone(t *testing.T) {
	sys := newMockSystem()
	sys.packageHandler.installed = map[string]string{}

	a := apply.New(sys, discardLogger, apply.Config{})

	err := a.Packages(context.Background(), []commonv1.Package{
		{Name: "gone", Ensure: "absent"},
	})
	require.NoError(t, err)
	require.NotContains(t, sys.packageHandler.removeCalls, "gone")
}

// --- Applier.Execs tests ---

func TestApplier_Execs_RunsOnSubscribedChange(t *testing.T) {
	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})

	execSet := []commonv1.Exec{
		{Command: "reload-thing", SusbscribeFiles: []string{"/etc/app.conf"}},
	}
	err := a.Execs(context.Background(), execSet, []string{"/etc/app.conf"})
	require.NoError(t, err)
	require.Equal(t, 1, sys.execHandler.calls["reload-thing"])
}

func TestApplier_Execs_SkipsWhenFileUnchanged(t *testing.T) {
	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})

	execSet := []commonv1.Exec{
		{Command: "reload-thing", SusbscribeFiles: []string{"/etc/app.conf"}},
	}
	err := a.Execs(context.Background(), execSet, []string{"/etc/other.conf"})
	require.NoError(t, err)
	require.Zero(t, sys.execHandler.calls["reload-thing"])
}

func TestApplier_Execs_DeduplicatesCommand(t *testing.T) {
	sys := newMockSystem()
	a := apply.New(sys, discardLogger, apply.Config{})

	// The same command subscribes to two files; both changed — should run once.
	execSet := []commonv1.Exec{
		{Command: "reload-thing", SusbscribeFiles: []string{"/etc/a.conf", "/etc/b.conf"}},
	}
	err := a.Execs(context.Background(), execSet, []string{"/etc/a.conf", "/etc/b.conf"})
	require.NoError(t, err)
	require.Equal(t, 1, sys.execHandler.calls["reload-thing"])
}

// --- mock system handler ---

var (
	_ handler.System         = (*mockSystem)(nil)
	_ handler.FileHandler    = (*mockFileHandler)(nil)
	_ handler.PackageHandler = (*mockPackageHandler)(nil)
	_ handler.ExecHandler    = (*mockExecHandler)(nil)
	_ handler.ServiceHandler = (*mockServiceHandler)(nil)
)

type mockSystem struct {
	fileHandler    *mockFileHandler
	packageHandler *mockPackageHandler
	execHandler    *mockExecHandler
	serviceHandler *mockServiceHandler
}

func newMockSystem() *mockSystem {
	return &mockSystem{
		fileHandler:    &mockFileHandler{writtenContent: make(map[string]string), removedPaths: make(map[string]bool)},
		packageHandler: &mockPackageHandler{installed: make(map[string]string), installCalls: make(map[string]int), removeCalls: make(map[string]int)},
		execHandler:    &mockExecHandler{calls: make(map[string]int)},
		serviceHandler: &mockServiceHandler{},
	}
}

func (m *mockSystem) Package() handler.PackageHandler { return m.packageHandler }
func (m *mockSystem) Service() handler.ServiceHandler { return m.serviceHandler }
func (m *mockSystem) File() handler.FileHandler       { return m.fileHandler }
func (m *mockSystem) Node() handler.NodeHandler       { return nil }
func (m *mockSystem) Exec() handler.ExecHandler       { return m.execHandler }

type mockFileHandler struct {
	writtenContent map[string]string
	removedPaths   map[string]bool
}

func (m *mockFileHandler) WriteContentFile(_ context.Context, path string, content []byte) (bool, error) {
	m.writtenContent[path] = string(content)
	return true, nil
}

func (m *mockFileHandler) Chown(_ context.Context, _, _, _ string) (bool, error) { return false, nil }
func (m *mockFileHandler) SetMode(_ context.Context, _, _ string) (bool, error)  { return false, nil }
func (m *mockFileHandler) Remove(_ context.Context, path string) (bool, error) {
	m.removedPaths[path] = true
	// Actually unlink so that follow-on operations (e.g. os.Symlink replacing
	// a regular file at this path) can succeed in tests using a real TempDir.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return true, err
	}
	return true, nil
}

type mockPackageHandler struct {
	installed    map[string]string
	installCalls map[string]int
	removeCalls  map[string]int
}

func (m *mockPackageHandler) List(_ context.Context) (map[string]string, error) {
	return m.installed, nil
}
func (m *mockPackageHandler) Install(_ context.Context, pkg, _ string) error {
	m.installCalls[pkg]++
	return nil
}
func (m *mockPackageHandler) Remove(_ context.Context, pkg string) error {
	m.removeCalls[pkg]++
	return nil
}
func (m *mockPackageHandler) UpgradeAll(_ context.Context) error { return nil }

type mockExecHandler struct {
	calls map[string]int
}

func (m *mockExecHandler) RunCommand(_ context.Context, cmd string, _ ...string) (string, int, error) {
	m.calls[cmd]++
	return "", 0, nil
}
func (m *mockExecHandler) SimpleRunCommand(_ context.Context, cmd string, _ ...string) error {
	m.calls[cmd]++
	return nil
}
func (m *mockExecHandler) RunCommandWithInput(_ context.Context, _ string, cmd string, _ ...string) (string, int, error) {
	m.calls[cmd]++
	return "", 0, nil
}

type mockServiceHandler struct{}

func (m *mockServiceHandler) Start(_ context.Context, _ string) error   { return nil }
func (m *mockServiceHandler) Stop(_ context.Context, _ string) error    { return nil }
func (m *mockServiceHandler) Restart(_ context.Context, _ string) error { return nil }
func (m *mockServiceHandler) Status(_ context.Context, _ string) (services.ServiceStatus, error) {
	return services.Stopped, nil
}
func (m *mockServiceHandler) Enable(_ context.Context, _ string) error          { return nil }
func (m *mockServiceHandler) Disable(_ context.Context, _ string) error         { return nil }
func (m *mockServiceHandler) SetArguments(_ context.Context, _, _ string) error { return nil }
