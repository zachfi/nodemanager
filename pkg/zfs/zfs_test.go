package zfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/handler"
)

func TestZfs(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		dataset     string
		err         error
		checkArgs   []string
		createArgs  []string
		destroyArgs []string
	}{
		{
			name:      "nope",
			status:    1,
			err:       ErrDatasetNotFound,
			checkArgs: []string{"list", "nope"},
		},

		{
			name:        "yep",
			status:      0,
			err:         nil,
			checkArgs:   []string{"list", "yep"},
			createArgs:  []string{"create", "yep"},
			destroyArgs: []string{"destroy", "yep"},
		},
	}

	for _, tc := range cases {
		var (
			ctx                     = context.Background()
			m   handler.ExecHandler = &handler.MockExecHandler{Status: tc.status}
			z                       = NewZfsManager(m)
			err error
		)

		err = z.Check(ctx, tc.name)
		require.Equal(t, tc.err, err)
		require.Equal(t, tc.checkArgs, m.(*handler.MockExecHandler).Recorder[zfsCmd])

		if tc.err != nil {
			continue
		}

		err = z.CreateDataset(ctx, tc.name)
		require.NoError(t, err)
		require.Equal(t, tc.createArgs, m.(*handler.MockExecHandler).Recorder[zfsCmd])

		err = z.DeleteDataset(ctx, tc.name)
		require.NoError(t, err)
		require.Equal(t, tc.destroyArgs, m.(*handler.MockExecHandler).Recorder[zfsCmd])

	}
}
