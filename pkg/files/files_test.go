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
		logHandler = slog.NewTextHandler(os.Stdout, nil)
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

		changed, err := h.Remove(tc.ctx, p)
		require.NoError(t, err, "removing a file that does not exist should not error")
		require.False(t, changed, "removing a file which does not exist should not be marked as changed")

		changed, err = h.WriteContentFile(tc.ctx, p, []byte("f"))
		require.NoError(t, err, "writing a file should not error")
		require.True(t, changed, "a file which was just created should be marked as changed")

		changed, err = h.WriteContentFile(tc.ctx, p, []byte("f"))
		require.NoError(t, err, "writing a file should not error")
		require.False(t, changed, "writing the same content to a file should not be marked as changed")

		changed, err = h.WriteContentFile(tc.ctx, p, []byte("ff"))
		require.NoError(t, err, "writing a file should not error")
		require.True(t, changed, "writing new content to a file should be marked as changed")

		fileInfo, err := os.Stat(p)
		require.NoError(t, err)

		var expectModeChange bool
		if fileInfo.Mode() != 0o600 {
			expectModeChange = true
		}

		changed, err = h.SetMode(tc.ctx, p, "0600")
		require.NoError(t, err)
		require.Equal(t, expectModeChange, changed)

		fileInfo, err = os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0o600), fileInfo.Mode())

		changed, err = h.SetMode(tc.ctx, p, "0400")
		require.NoError(t, err, "setting th emode should not error")
		require.True(t, changed, "when the mode changes the file should be marked as changed")

		fileInfo, err = os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0o400), fileInfo.Mode())

		// TODO: Can we reasonably test a chown?
	}
}
