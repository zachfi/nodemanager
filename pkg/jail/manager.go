package jail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

// Manager provisions and removes FreeBSD jails backed by ZFS datasets.
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
	// DeleteJail tears down the jail's ZFS datasets and config files.
	DeleteJail(ctx context.Context, j freebsdv1.Jail) error
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
func NewManager(ctx context.Context, basePath, zfsDataset string, exec handler.ExecHandler) (Manager, error) {
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

	// 5. Write per-jail fstab if mounts are declared.
	fstabPath := ""
	if len(j.Spec.Mounts) > 0 {
		fstabPath = filepath.Join(m.basePath, JailRootDir, j.Name, "fstab")
		if err := writeFstab(fstabPath, jailRoot, j.Spec.Mounts); err != nil {
			return fmt.Errorf("writing fstab for %s: %w", j.Name, err)
		}
	}

	// 6. Write <confDir>/<name>.conf.
	if err := writeJailConf(m.confDir, j.Name, jailRoot, fstabPath, j.Spec); err != nil {
		return fmt.Errorf("writing jail.conf for %s: %w", j.Name, err)
	}

	return nil
}

// DeleteJail destroys the jail's ZFS datasets and removes its config files.
// The release dataset and its snapshot are left in place for reuse by other
// jails on the same release.
func (m *manager) DeleteJail(ctx context.Context, j freebsdv1.Jail) error {
	// Remove config files first so the jail cannot be started accidentally
	// during teardown.
	if err := removeJailConf(m.confDir, j.Name); err != nil {
		return err
	}

	fstabPath := filepath.Join(m.basePath, JailRootDir, j.Name, "fstab")
	if err := removeFstab(fstabPath); err != nil {
		return err
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
