package jail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/zfs"
)

const (
	// JailRootDir is the subdirectory under basePath that holds per-jail datasets.
	JailRootDir = "jails"
	// ReleaseRootDir is the subdirectory under basePath that holds release datasets.
	ReleaseRootDir = "releases"
)

// Manager provisions, removes, and controls the lifecycle of FreeBSD jails
// backed by ZFS datasets.
//
// Dataset layout (relative to the configured base dataset, e.g. zroot/nodemanager):
//
//	<dataset>/releases/<version>          — extracted FreeBSD base (one per release)
//	<dataset>/jails/<name>                — per-jail container dataset
//	<dataset>/jails/<name>/root           — ZFS clone of the release snapshot
//
// Filesystem layout (relative to basePath, e.g. /usr/local/nodemanager):
//
//	releases/<version>/                   — release root (bin/freebsd-version present when ready)
//	jails/<name>/                         — per-jail directory (fstab, logs)
//	jails/<name>/root/                    — jail root filesystem (mounted clone)
type Manager interface {
	// EnsureJail idempotently provisions the jail: release, ZFS datasets,
	// jail root population, jail.conf, and fstab.
	EnsureJail(ctx context.Context, j freebsdv1.Jail) error
	// DeleteJail stops the jail (if running) then tears down its ZFS datasets
	// and config files.
	DeleteJail(ctx context.Context, j freebsdv1.Jail) error
	// StartJail starts a provisioned jail via `jail -c`.
	StartJail(ctx context.Context, name string) error
	// StopJail stops a running jail via `jail -r`.
	StopJail(ctx context.Context, name string) error
	// RestartJail stops and then starts a jail.
	RestartJail(ctx context.Context, name string) error
	// IsRunning reports whether the named jail is currently active according
	// to jls(8).
	IsRunning(ctx context.Context, name string) (bool, error)
	// InstalledRelease returns the FreeBSD release string from the jail root
	// (reads /bin/freebsd-version).  Used to populate status.release and to
	// detect when spec.release differs from the provisioned base.
	InstalledRelease(jailRoot string) (string, error)
	// UpdateJail runs freebsd-update(8) against the jail root to apply
	// patch-level security updates within the current release.  The jail must
	// be stopped before calling this.
	UpdateJail(ctx context.Context, jailRoot string) error
	// ExecInJail runs a command inside a running jail via jexec(8).
	ExecInJail(ctx context.Context, jailName, command string, args ...string) error
	// BootstrapPkg installs the pkg(8) package manager inside the jail if
	// it is not already present.  The jail must be running.
	BootstrapPkg(ctx context.Context, jailName, jailRoot string) error
}

var _ Manager = (*manager)(nil)

type manager struct {
	// basePath is the root filesystem path, e.g. /usr/local/nodemanager.
	basePath string
	// dataset is the root ZFS dataset name, e.g. zroot/nodemanager.
	dataset string
	// confDir is where per-jail .conf fragments are written.
	confDir string

	zfs      zfs.Manager
	exec     handler.ExecHandler
	releases ReleaseManager
}

// NewManager initialises the manager, ensuring the base ZFS dataset layout
// exists. It does not start or modify any jails.
func NewManager(ctx context.Context, basePath, zfsDataset, mirror string, exec handler.ExecHandler) (Manager, error) {
	zfsManager := zfs.NewZfsManager(exec)

	// Base dataset with explicit mountpoint.
	if err := zfsManager.Ensure(ctx, zfsDataset, "mountpoint="+basePath); err != nil {
		return nil, fmt.Errorf("ensuring base dataset %s: %w", zfsDataset, err)
	}

	// Child datasets inherit the mountpoint from the parent hierarchy.
	for _, sub := range []string{JailRootDir, ReleaseRootDir} {
		ds := filepath.Join(zfsDataset, sub)
		if err := zfsManager.Ensure(ctx, ds); err != nil {
			return nil, fmt.Errorf("ensuring dataset %s: %w", ds, err)
		}
	}

	releases := newReleaseManager(
		filepath.Join(basePath, ReleaseRootDir),
		filepath.Join(zfsDataset, ReleaseRootDir),
		mirror,
		zfsManager,
		exec,
	)

	return &manager{
		basePath: basePath,
		dataset:  zfsDataset,
		confDir:  DefaultJailConfDir,
		zfs:      zfsManager,
		exec:     exec,
		releases: releases,
	}, nil
}

// EnsureJail provisions the jail described by j. It is safe to call multiple
// times; each step is skipped when already in the desired state.
func (m *manager) EnsureJail(ctx context.Context, j freebsdv1.Jail) error {
	// 1. Ensure the FreeBSD release is downloaded and extracted.
	if err := m.releases.Ensure(ctx, j.Spec.Release); err != nil {
		return fmt.Errorf("ensuring release %s: %w", j.Spec.Release, err)
	}

	// 2. Ensure the per-jail container dataset exists (holds fstab, logs, etc.).
	jailDataset := filepath.Join(m.dataset, JailRootDir, j.Name)
	if err := m.zfs.Ensure(ctx, jailDataset); err != nil {
		return fmt.Errorf("ensuring jail dataset %s: %w", jailDataset, err)
	}

	// 3. Clone the release to the jail root if it does not already exist.
	//    This is a ZFS CoW clone, so it is nearly instantaneous and
	//    space-efficient until the jail diverges from the release.
	jailRootDataset := filepath.Join(m.dataset, JailRootDir, j.Name, "root")
	jailRoot := filepath.Join(m.basePath, JailRootDir, j.Name, "root")

	exists, err := m.zfs.Exists(ctx, jailRootDataset)
	if err != nil {
		return fmt.Errorf("checking jail root dataset: %w", err)
	}

	if exists {
		// Check whether the existing clone was built from spec.release.
		// Read origin before any destructive operation.
		origin, err := m.zfs.GetProperty(ctx, jailRootDataset, "origin")
		if err != nil {
			return fmt.Errorf("checking jail root origin for %s: %w", j.Name, err)
		}
		if origin != "" && origin != "-" && releaseFromOrigin(origin) != j.Spec.Release {
			// User changed spec.release — reprovision from the new base.
			_ = m.StopJail(ctx, j.Name)
			if err := m.zfs.DestroyDatasetRecursive(ctx, jailRootDataset); err != nil {
				return fmt.Errorf("destroying stale jail root for %s: %w", j.Name, err)
			}
			// Best-effort removal of the old release snapshot created for this jail.
			_ = m.zfs.DestroyDataset(ctx, origin)
			exists = false
		}
	}

	if !exists {
		releaseDataset := filepath.Join(m.dataset, ReleaseRootDir, j.Spec.Release)
		snapshot := fmt.Sprintf("%s@%s", releaseDataset, j.Name)

		if err := m.zfs.Snapshot(ctx, releaseDataset, j.Name); err != nil {
			return fmt.Errorf("snapshotting release for %s: %w", j.Name, err)
		}
		if err := m.zfs.Clone(ctx, snapshot, jailRootDataset, "mountpoint="+jailRoot); err != nil {
			return fmt.Errorf("cloning jail root for %s: %w", j.Name, err)
		}
	}

	// 4. Copy essential host files into the jail root.
	if err := m.copyHostFiles(jailRoot); err != nil {
		return fmt.Errorf("copying host files into jail %s: %w", j.Name, err)
	}

	// 5. Write per-jail fstab and ensure mountpoint directories exist.
	fstabPath := ""
	if len(j.Spec.Mounts) > 0 {
		fstabPath = filepath.Join(m.basePath, JailRootDir, j.Name, "fstab")
		if err := writeFstab(fstabPath, jailRoot, j.Spec.Mounts); err != nil {
			return fmt.Errorf("writing fstab for %s: %w", j.Name, err)
		}
		for _, mount := range j.Spec.Mounts {
			mp := filepath.Join(jailRoot, mount.JailPath)
			if err := os.MkdirAll(mp, 0o755); err != nil {
				return fmt.Errorf("creating mountpoint %s for jail %s: %w", mp, j.Name, err)
			}
		}
	}

	// 6. Write <confDir>/<name>.conf.
	if err := writeJailConf(m.confDir, j.Name, jailRoot, fstabPath, j.Spec); err != nil {
		return fmt.Errorf("writing jail.conf for %s: %w", j.Name, err)
	}

	return nil
}

// DeleteJail stops the jail (if running), then destroys its ZFS datasets and
// removes its config files. The release dataset and its snapshot are left in
// place for reuse by other jails on the same release.
func (m *manager) DeleteJail(ctx context.Context, j freebsdv1.Jail) error {
	// Stop the jail gracefully before tearing down its filesystem. Ignore
	// errors here — the jail may already be stopped.
	_ = m.StopJail(ctx, j.Name)

	// Remove config files so the jail cannot be started accidentally
	// during teardown.
	if err := removeJailConf(m.confDir, j.Name); err != nil {
		return err
	}

	fstabPath := filepath.Join(m.basePath, JailRootDir, j.Name, "fstab")
	if err := removeFstab(fstabPath); err != nil {
		return err
	}

	// Unmount any remaining nullfs/devfs mounts under the jail root before
	// destroying ZFS datasets.  jail -r normally handles this, but if the
	// jail was never started or the stop failed, mounts may linger.
	jailRoot := filepath.Join(m.basePath, JailRootDir, j.Name, "root")
	if err := m.unmountAll(ctx, jailRoot); err != nil {
		return fmt.Errorf("unmounting jail filesystems for %s: %w", j.Name, err)
	}

	// Recursively destroy jails/<name> and all child datasets (including root).
	jailDataset := filepath.Join(m.dataset, JailRootDir, j.Name)
	exists, err := m.zfs.Exists(ctx, jailDataset)
	if err != nil {
		return fmt.Errorf("checking jail dataset: %w", err)
	}
	if exists {
		if err := m.zfs.DestroyDatasetRecursive(ctx, jailDataset); err != nil {
			return fmt.Errorf("destroying jail dataset %s: %w", jailDataset, err)
		}
	}

	// Clean up the release snapshot created for this jail.
	if j.Spec.Release != "" {
		snapshot := fmt.Sprintf("%s/%s/%s@%s", m.dataset, ReleaseRootDir, j.Spec.Release, j.Name)
		if err := m.zfs.DestroyDataset(ctx, snapshot); err != nil {
			return fmt.Errorf("destroying release snapshot %s: %w", snapshot, err)
		}
	}

	return nil
}

func (m *manager) StartJail(ctx context.Context, name string) error {
	confPath := filepath.Join(m.confDir, name+".conf")
	return m.exec.SimpleRunCommand(ctx, "jail", "-c", "-f", confPath, name)
}

func (m *manager) StopJail(ctx context.Context, name string) error {
	return m.exec.SimpleRunCommand(ctx, "jail", "-r", name)
}

func (m *manager) RestartJail(ctx context.Context, name string) error {
	if err := m.StopJail(ctx, name); err != nil {
		return err
	}
	return m.StartJail(ctx, name)
}

func (m *manager) IsRunning(ctx context.Context, name string) (bool, error) {
	return isJailRunning(ctx, m.exec, name)
}

func (m *manager) InstalledRelease(jailRoot string) (string, error) {
	return installedRelease(jailRoot)
}

func (m *manager) ExecInJail(ctx context.Context, jailName, command string, args ...string) error {
	cmdArgs := append([]string{jailName, command}, args...)
	return m.exec.SimpleRunCommand(ctx, "jexec", cmdArgs...)
}

func (m *manager) BootstrapPkg(ctx context.Context, jailName, jailRoot string) error {
	pkgPath := filepath.Join(jailRoot, "usr/local/sbin/pkg")
	if _, err := os.Stat(pkgPath); err == nil {
		return nil
	}
	return m.ExecInJail(ctx, jailName, "env", "ASSUME_ALWAYS_YES=yes", "pkg", "bootstrap")
}

// UpdateJail runs freebsd-update(8) against the jail root to apply patch-level
// security updates.  The jail should be stopped before calling this.
func (m *manager) UpdateJail(ctx context.Context, jailRoot string) error {
	return m.exec.SimpleRunCommand(ctx, "env", "PAGER=cat",
		"/usr/sbin/freebsd-update", "-b", jailRoot, "--not-running-from-cron", "fetch", "install")
}

// releaseFromOrigin extracts the FreeBSD release component from a ZFS clone
// origin property.  Origin has the form:
//
//	<baseDataset>/releases/<version>@<jailName>
//
// e.g. "zroot/nodemanager/releases/14.2-RELEASE@web01" → "14.2-RELEASE".
func releaseFromOrigin(origin string) string {
	// Drop the @<jailName> snapshot suffix.
	datasetPart := strings.SplitN(origin, "@", 2)[0]
	// The last path component is the release version.
	return filepath.Base(datasetPart)
}

// installedRelease reads the USERLAND_VERSION from /bin/freebsd-version inside
// the jail root without executing any code — just string parsing.
func installedRelease(jailRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(jailRoot, "bin", "freebsd-version"))
	if err != nil {
		return "", fmt.Errorf("reading freebsd-version: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "USERLAND_VERSION=") {
			continue
		}
		v := strings.TrimPrefix(line, "USERLAND_VERSION=")
		v = strings.Trim(v, `"`)
		return v, nil
	}
	return "", fmt.Errorf("USERLAND_VERSION not found in %s/bin/freebsd-version", jailRoot)
}

// copyHostFiles copies /etc/resolv.conf and /etc/localtime into the jail root
// so that it has working DNS and correct timezone immediately after creation.
func (m *manager) copyHostFiles(jailRoot string) error {
	for _, f := range []string{"/etc/resolv.conf", "/etc/localtime"} {
		data, err := os.ReadFile(f)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("reading %s: %w", f, err)
		}
		dest := filepath.Join(jailRoot, f)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", dest, err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}
	return nil
}

// unmountAll finds and unmounts all filesystems mounted under jailRoot.
// Mounts are unmounted deepest-first to avoid "device busy" errors.
func (m *manager) unmountAll(ctx context.Context, jailRoot string) error {
	output, _, err := m.exec.RunCommand(ctx, "mount", "-p")
	if err != nil {
		return fmt.Errorf("listing mounts: %w", err)
	}

	// Collect mountpoints under jailRoot.
	prefix := jailRoot + "/"
	var mounts []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mp := fields[1]
		if mp == jailRoot || strings.HasPrefix(mp, prefix) {
			mounts = append(mounts, mp)
		}
	}

	// Sort in reverse so deeper paths are unmounted first.
	slices.SortFunc(mounts, func(a, b string) int {
		if len(a) > len(b) {
			return -1
		}
		if len(a) < len(b) {
			return 1
		}
		return strings.Compare(a, b)
	})

	for _, mp := range mounts {
		if err := m.exec.SimpleRunCommand(ctx, "umount", "-f", mp); err != nil {
			return fmt.Errorf("unmounting %s: %w", mp, err)
		}
	}

	return nil
}
