package files

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFiles(t *testing.T) {
	var (
		logHandler = slog.NewTextHandler(t.Output(), nil)
		logger     = slog.New(logHandler)
	)

	cases := []struct {
		ctx                        context.Context
		defaultOwner, defaultGroup string
	}{
		{
			ctx: context.Background(),
		},
	}

	for _, tc := range cases {
		tmp := t.TempDir()
		p := tmp + "/f"

		h := New(logger, tc.defaultOwner, tc.defaultGroup)

		err := h.Remove(tc.ctx, p)
		require.NoError(t, err, "removing a file that does not exist should not error")

		err = h.WriteContentFile(tc.ctx, p, []byte("f"))
		require.NoError(t, err)

		err = h.SetMode(tc.ctx, p, "0600")
		require.NoError(t, err)

		fileInfo, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0o600), fileInfo.Mode())

		err = h.SetMode(tc.ctx, p, "0400")
		require.NoError(t, err)

		fileInfo, err = os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0o400), fileInfo.Mode())
	}
}
