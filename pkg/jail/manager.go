package jail

import (
	"context"
	"os"
	"path/filepath"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/zfs"
)

const zfsCmd = "/sbin/zfs"

// Manager is the interface for managing the creation and deletion of jails on FreeBSD.
// - creates the directory structure for the jail
// - fetches the release if not already present
// - sets up the jail configuration
type Manager interface {
	CreateJail(ctx context.Context, j freebsdv1.Jail) error
	DeleteJail(ctx context.Context, name string) error
	ensureRelease(ctx context.Context, release string) error
	extractRelease(ctx context.Context, release, dest string) error
	deleteRelease(ctx context.Context, release string) error
}

var _ Manager = (*manager)(nil)

const (
	// JailRootDir is the name of the root directory for jails within the manager's data path.
	JailRootDir = "jails"
	// ReleaseRootDir is the name of the root directory for releases within the manager's	 data path.
	ReleaseRootDir = "releases"
)

// manager implements the Manager interface for FreeBSD jails.
type manager struct {
	// The path where data for the manager is stored.
	dir string
	// The ZFS dataset used for storing nodemanager data.
	dataset string

	exec       handler.ExecHandler
	zfsManager zfs.Manager
}

func NewManager(ctx context.Context, basePath string, zfsDataset string, exec handler.ExecHandler) (Manager, error) {
	zfsManager := zfs.NewZfsManager(exec)

	// Ensure the base ZFS dataset exists
	err := zfsManager.Check(ctx, zfsDataset)
	if err != nil {
		if err == zfs.ErrDatasetNotFound {
			err = zfsManager.CreateDataset(ctx, zfsDataset, "mountpoint="+basePath)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// Create additional datasets for releases and jails
	additionalDataSets := []string{JailRootDir, ReleaseRootDir}
	for _, ds := range additionalDataSets {
		fullDS := zfsDataset + "/" + ds
		err = zfsManager.Check(ctx, fullDS)
		if err != nil {
			if err == zfs.ErrDatasetNotFound {
				err = zfsManager.CreateDataset(ctx, fullDS)
				if err != nil {
					return nil, err
				}
			} else {
				return nil, err
			}
		}
	}

	return &manager{
		dir:        basePath,
		dataset:    zfsDataset,
		exec:       exec,
		zfsManager: zfsManager,
	}, nil
}

func (m *manager) CreateJail(ctx context.Context, j freebsdv1.Jail) error {
	// Check and create the necessary ZFS dataset for the jail
	jailDataset := filepath.Join(m.dir, JailRootDir, j.Name)
	err := m.zfsManager.Check(ctx, jailDataset)
	if err != nil {
		if err == zfs.ErrDatasetNotFound {
			err = m.zfsManager.CreateDataset(ctx, jailDataset)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	err = m.extractRelease(ctx, j.Spec.Release, filepath.Join(m.dir, ReleaseRootDir, j.Spec.Release))
	if err != nil {
		return err
	}

	// Check the release specified in the jail spec.
	// If the release does not exist, create it.

	// Check if the path for the jail exists.  Path specified as m.dir/name/root
	// use zfs
	// If it does, return nil indicating that the jail already exists.
	// If it does not, create the jail structure from the given release
	// If the release is not found, fetch the release

	return nil
}

func (m *manager) DeleteJail(ctx context.Context, name string) error {
	// Implementation to delete the specified jail
	return nil
}

// ensureRelease creates a FreeBSD release for use in jails.
// - fetches the release if not already present
func (m *manager) ensureRelease(ctx context.Context, release string) error {
	// Implementation to create the specified release

	// Create the release dataset if it does not exist
	var (
		releasePath    = filepath.Join(m.dir, ReleaseRootDir, release)
		releaseDataset = filepath.Join(m.dataset, ReleaseRootDir, release)
	)
	err := m.zfsManager.Check(ctx, releaseDataset)
	if err != nil {
		if err == zfs.ErrDatasetNotFound {
			err = m.zfsManager.CreateDataset(ctx, releaseDataset)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// Fetch the release is not already present
	_, err = os.Stat(releasePath)
	if os.IsNotExist(err) {
		// TODO: Fetch the release
	}

	return nil
}

// ExtractRelease extracts the specified FreeBSD release to the given destination.
func (m *manager) extractRelease(ctx context.Context, release, dest string) error {
	// Implementation to extract the specified release to the destination
	return nil
}

func (m *manager) deleteRelease(ctx context.Context, release string) error {
	// Implementation to delete the specified release
	return nil
}
