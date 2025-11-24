package zfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/handler"
)

func TestZfsInternal(t *testing.T) {
	cases := []struct {
		name        string
		status      []int
		dataset     string
		err         error
		options     []string
		checkArgs   []string
		createArgs  []string
		destroyArgs []string
	}{
		{
			name:      "nope",
			status:    []int{1},
			err:       ErrDatasetNotFound,
			checkArgs: []string{"list", "nope"},
		},

		{
			name:        "yep",
			status:      []int{0},
			err:         nil,
			checkArgs:   []string{"list", "yep"},
			createArgs:  []string{"create", "yep"},
			destroyArgs: []string{"destroy", "yep"},
		},
		{
			name:        "withopts",
			status:      []int{0},
			err:         nil,
			options:     []string{"mountpoint=/data/jails", "compression=lz4"},
			checkArgs:   []string{"list", "withopts"},
			createArgs:  []string{"create", "withopts", "-o", "mountpoint=/data/jails", "-o", "compression=lz4"},
			destroyArgs: []string{"destroy", "withopts"},
		},
	}

	for _, tc := range cases {
		var (
			ctx                     = context.Background()
			m   handler.ExecHandler = &handler.MockExecHandler{Status: tc.status}
			z                       = NewZfsManager(m)
			err error
		)

		err = z.check(ctx, tc.name)
		require.Equal(t, tc.err, err)
		require.Equal(t, tc.checkArgs, m.(*handler.MockExecHandler).Recorder[zfsCmd][0])

		if tc.err != nil {
			continue
		}

		err = z.createDataset(ctx, tc.name, tc.options...)
		require.NoError(t, err)
		require.Equal(t, tc.createArgs, m.(*handler.MockExecHandler).Recorder[zfsCmd][1])

		err = z.DestroyDataset(ctx, tc.name)
		require.NoError(t, err)
		require.Equal(t, tc.destroyArgs, m.(*handler.MockExecHandler).Recorder[zfsCmd][2])

	}
}

func TestZfsInterface(t *testing.T) {
	cases := []struct {
		name       string
		status     []int
		options    []string
		ensureArgs [][]string
	}{
		{
			name:       "alpha",
			ensureArgs: [][]string{{"list", "alpha"}, {"create", "alpha"}},
			status:     []int{1},
		},
		{
			name:       "bravo",
			ensureArgs: [][]string{{"list", "bravo"}},
			status:     []int{0},
		},
		{
			name:       "charlie",
			ensureArgs: [][]string{{"list", "charlie"}, {"create", "charlie", "-o", "mountpoint=/data/charlie", "-o", "compression=lz4"}},
			status:     []int{1},
			options:    []string{"mountpoint=/data/charlie", "compression=lz4"},
		},
	}

	for _, tc := range cases {
		var (
			ctx                     = context.Background()
			m   handler.ExecHandler = &handler.MockExecHandler{Status: tc.status}
			z                       = NewZfsManager(m)
			err error
		)

		err = z.Ensure(ctx, tc.name, tc.options...)
		require.Nil(t, err)
		require.Equal(t, tc.ensureArgs, m.(*handler.MockExecHandler).Recorder[zfsCmd])

	}
}
