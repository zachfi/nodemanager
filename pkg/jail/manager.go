package jail

import (
	"context"

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
	createRelease(ctx context.Context, release string) error
	extractRelease(ctx context.Context, release, dest string) error
	deleteRelease(ctx context.Context, release string) error
}

var _ Manager = (*manager)(nil)

// manager implements the Manager interface for FreeBSD jails.
type manager struct {
	// The path where data for the manager is stored.
	dir string

	exec       handler.ExecHandler
	zfsManager zfs.Manager
}

func NewManager(ctx context.Context, basePath string, zfsDataset string, exec handler.ExecHandler) (Manager, error) {
	zfsManager := zfs.NewZfsManager(exec)
	err := zfsManager.Check(ctx, zfsDataset)
	if err != nil {
		if err == zfs.ErrDatasetNotFound {
			err = zfsManager.CreateDataset(ctx, zfsDataset, basePath)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// FIXME: instead of mkdir, set the mountpoint of the zfs dataset to basePath
	// err = os.MkdirAll(basePath, 0x700)
	// if err != nil {
	// 	return nil, err
	// }

	return &manager{
		dir:        basePath,
		exec:       exec,
		zfsManager: zfsManager,
	}, nil
}

func (m *manager) CreateJail(ctx context.Context, j freebsdv1.Jail) error {
	// Implementation to create a jail using the specified template

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

// CreateRelease creates a FreeBSD release for use in jails.
// - fetches the release if not already present
func (m *manager) createRelease(ctx context.Context, release string) error {
	// Implementation to create the specified release
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
