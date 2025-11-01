package jail

import (
	"os"

	"github.com/zachfi/nodemanager/pkg/handler"
)

const zfsCmd = "/sbin/zfs"

// Manager is the interface for managing the creation and deletion of jails on FreeBSD.
// - creates the directory structure for the jail
// - fetches the release if not already present
// - sets up the jail configuration
type Manager interface {
	CreateJail(name, template, release string) error
	DeleteJail(name string) error
	CreateRelease(release string) error
	ExtractRelease(release, dest string) error
	DeleteRelease(release string) error
}

var _ Manager = (*manager)(nil)

// manager implements the Manager interface for FreeBSD jails.
type manager struct {
	// The path where data for the manager is stored.
	dir string

	exec handler.ExecHandler
}

func NewManager(dir string, exec handler.ExecHandler) (Manager, error) {
	// TODO: use ZFS if available for better performance and snapshot capabilities

	err := os.MkdirAll(dir, 0x700)
	if err != nil {
		return nil, err
	}

	return &manager{
		dir:  dir,
		exec: exec,
	}, nil
}

func (m *manager) CreateJail(name, template, release string) error {
	// Implementation to create a jail using the specified template

	// Check if the path for the jail exists.  Path specified as m.dir/name/root
	// use zfs
	// If it does, return nil indicating that the jail already exists.
	// If it does not, create the jail structure from the given release
	// If the release is not found, fetch the release

	return nil
}

func (m *manager) DeleteJail(name string) error {
	// Implementation to delete the specified jail
	return nil
}

// CreateRelease creates a FreeBSD release for use in jails.
// - fetches the release if not already present
func (m *manager) CreateRelease(release string) error {
	// Implementation to create the specified release
	return nil
}

// ExtractRelease extracts the specified FreeBSD release to the given destination.
func (m *manager) ExtractRelease(release, dest string) error {
	// Implementation to extract the specified release to the destination
	return nil
}

func (m *manager) DeleteRelease(release string) error {
	// Implementation to delete the specified release
	return nil
}
