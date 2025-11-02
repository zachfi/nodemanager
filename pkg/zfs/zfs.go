package zfs

import (
	"context"

	"github.com/zachfi/nodemanager/pkg/handler"
)

const zfsCmd = "/sbin/zfs"

type Manager interface {
	Check(ctx context.Context, datasetName string) error
	CreateDataset(ctx context.Context, datasetName string, opts ...string) error
	DeleteDataset(ctx context.Context, datasetName string) error
}

var _ Manager = (*zfsManager)(nil)

type zfsManager struct {
	exec handler.ExecHandler
}

func NewZfsManager(exec handler.ExecHandler) Manager {
	return &zfsManager{exec}
}

func (z *zfsManager) Check(ctx context.Context, datasetName string) error {
	// zfs list <name>
	_, e, err := z.exec.RunCommand(ctx, zfsCmd, "list", datasetName)
	if e == 1 {
		// dataset does not exist
		return ErrDatasetNotFound
	}

	return err
}

func (z *zfsManager) CreateDataset(ctx context.Context, name string, opts ...string) error {
	// zfs create <name>

	options := make([]string, 0, len(opts)+2)
	options = append(options, "create")
	options = append(options, name)
	for _, o := range opts {
		options = append(options, "-o", o)
	}

	return z.exec.SimpleRunCommand(ctx, zfsCmd, options...)
}

func (z *zfsManager) DeleteDataset(ctx context.Context, name string) error {
	// zfs destroy <name>
	return z.exec.SimpleRunCommand(ctx, zfsCmd, "destroy", name)
}
