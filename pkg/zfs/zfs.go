package zfs

import (
	"context"

	"github.com/zachfi/nodemanager/pkg/handler"
)

const zfsCmd = "/sbin/zfs"

type Manager interface {
	// ListDataset checks if a ZFS dataset with the given name exists.
	Check(ctx context.Context, name string) error
	CreateDataset(ctx context.Context, name string) error
	DeleteDataset(ctx context.Context, name string) error
}

var _ Manager = (*zfsManager)(nil)

type zfsManager struct {
	exec handler.ExecHandler
}

func NewZfsManager(ctx context.Context, exec handler.ExecHandler) Manager {
	return &zfsManager{exec}
}

func (z *zfsManager) Check(ctx context.Context, name string) error {
	// zfs list <name>
	_, e, err := z.exec.RunCommand(ctx, zfsCmd, "list", name)
	if e == 1 {
		// dataset does not exist
		return ErrDatasetNotFound
	}

	return err
}

func (z *zfsManager) CreateDataset(ctx context.Context, name string) error {
	// zfs create <name>
	return z.exec.SimpleRunCommand(ctx, zfsCmd, "create", name)
}

func (z *zfsManager) DeleteDataset(ctx context.Context, name string) error {
	// zfs destroy <name>
	return z.exec.SimpleRunCommand(ctx, zfsCmd, "destroy", name)
}
