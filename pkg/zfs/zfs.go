package zfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/zachfi/nodemanager/pkg/handler"
)

const zfsCmd = "/sbin/zfs"

// Manager provides idempotent ZFS dataset operations.
type Manager interface {
	// Ensure creates the dataset if it does not already exist.
	Ensure(ctx context.Context, dataset string, opts ...string) error
	// Exists reports whether the dataset exists.
	Exists(ctx context.Context, dataset string) (bool, error)
	// GetProperty returns the value of a single ZFS property for the dataset.
	// Returns "-" when the property has no value (e.g. origin on a non-clone).
	GetProperty(ctx context.Context, dataset, property string) (string, error)
	// Snapshot creates a snapshot of the dataset named <dataset>@<name>.
	Snapshot(ctx context.Context, dataset, name string) error
	// Clone creates a new dataset cloned from the given snapshot.
	Clone(ctx context.Context, snapshot, target string, opts ...string) error
	// DestroyDataset destroys a single dataset (no dependents).
	DestroyDataset(ctx context.Context, dataset string) error
	// DestroyDatasetRecursive destroys a dataset and all its children.
	DestroyDatasetRecursive(ctx context.Context, dataset string) error
}

var _ Manager = (*zfsManager)(nil)

type zfsManager struct {
	exec handler.ExecHandler
}

func NewZfsManager(exec handler.ExecHandler) Manager {
	return &zfsManager{exec}
}

func (z *zfsManager) GetProperty(ctx context.Context, dataset, property string) (string, error) {
	out, _, err := z.exec.RunCommand(ctx, zfsCmd, "get", "-H", "-o", "value", property, dataset)
	if err != nil {
		return "", fmt.Errorf("zfs get %s %s: %w", property, dataset, err)
	}
	return strings.TrimSpace(out), nil
}

func (z *zfsManager) Exists(ctx context.Context, dataset string) (bool, error) {
	_, e, err := z.exec.RunCommand(ctx, zfsCmd, "list", dataset)
	if e == 1 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// createDataset runs: zfs create [-o key=val ...] <dataset>
func (z *zfsManager) createDataset(ctx context.Context, dataset string, opts ...string) error {
	args := make([]string, 0, len(opts)*2+2)
	args = append(args, "create")
	for _, o := range opts {
		args = append(args, "-o", o)
	}
	args = append(args, dataset)
	return z.exec.SimpleRunCommand(ctx, zfsCmd, args...)
}

func (z *zfsManager) Ensure(ctx context.Context, dataset string, opts ...string) error {
	exists, err := z.Exists(ctx, dataset)
	if err != nil {
		return err
	}
	if !exists {
		return z.createDataset(ctx, dataset, opts...)
	}
	return nil
}

func (z *zfsManager) Snapshot(ctx context.Context, dataset, name string) error {
	return z.exec.SimpleRunCommand(ctx, zfsCmd, "snapshot", fmt.Sprintf("%s@%s", dataset, name))
}

// Clone runs: zfs clone [-o key=val ...] <snapshot> <target>
func (z *zfsManager) Clone(ctx context.Context, snapshot, target string, opts ...string) error {
	args := make([]string, 0, len(opts)*2+3)
	args = append(args, "clone")
	for _, o := range opts {
		args = append(args, "-o", o)
	}
	args = append(args, snapshot, target)
	return z.exec.SimpleRunCommand(ctx, zfsCmd, args...)
}

func (z *zfsManager) DestroyDataset(ctx context.Context, dataset string) error {
	return z.exec.SimpleRunCommand(ctx, zfsCmd, "destroy", dataset)
}

func (z *zfsManager) DestroyDatasetRecursive(ctx context.Context, dataset string) error {
	return z.exec.SimpleRunCommand(ctx, zfsCmd, "destroy", "-r", dataset)
}
