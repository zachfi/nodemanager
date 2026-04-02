package files

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestSaveToFileBucket(t *testing.T) {
	bucket := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "test.conf")

	content := []byte("hello filebucket")
	require.NoError(t, os.WriteFile(srcPath, content, 0o644))

	info, err := os.Stat(srcPath)
	require.NoError(t, err)

	// First save.
	hash, err := SaveToFileBucket(bucket, srcPath, content, info)
	require.NoError(t, err)
	require.Len(t, hash, 64, "SHA256 hex should be 64 chars")

	// Verify blob path.
	blobPath := filepath.Join(bucket, hash[0:2], hash[2:4], hash[4:])
	blob, err := os.ReadFile(blobPath)
	require.NoError(t, err)
	require.Equal(t, content, blob)

	// Verify blob permissions.
	blobInfo, err := os.Stat(blobPath)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o600), blobInfo.Mode().Perm())

	// Verify dir permissions.
	dirInfo, err := os.Stat(filepath.Dir(blobPath))
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o700), dirInfo.Mode().Perm())

	// Verify meta sidecar.
	metaBytes, err := os.ReadFile(blobPath + ".meta")
	require.NoError(t, err)
	var meta FileBucketMeta
	require.NoError(t, json.Unmarshal(metaBytes, &meta))
	require.Equal(t, srcPath, meta.Path)
	require.NotEmpty(t, meta.BackedUpAt)
	require.NotEmpty(t, meta.Mode)

	// Idempotent: second save with same content returns same hash without rewriting.
	blobInfo1, _ := os.Stat(blobPath)
	hash2, err := SaveToFileBucket(bucket, srcPath, content, info)
	require.NoError(t, err)
	require.Equal(t, hash, hash2)
	blobInfo2, _ := os.Stat(blobPath)
	require.Equal(t, blobInfo1.ModTime(), blobInfo2.ModTime(), "blob mtime should not change on repeat call")

	// Different content → different hash.
	hash3, err := SaveToFileBucket(bucket, srcPath, []byte("other content"), info)
	require.NoError(t, err)
	require.NotEqual(t, hash, hash3)
}

func TestGCFileBucket(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	bucket := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "test.conf")

	writeBlob := func(content []byte) string {
		require.NoError(t, os.WriteFile(srcPath, content, 0o644))
		info, err := os.Stat(srcPath)
		require.NoError(t, err)
		hash, err := SaveToFileBucket(bucket, srcPath, content, info)
		require.NoError(t, err)
		return hash
	}

	oldHash := writeBlob([]byte("old content"))
	newHash := writeBlob([]byte("new content"))

	// Back-date the old blob's mtime.
	oldBlob := filepath.Join(bucket, oldHash[0:2], oldHash[2:4], oldHash[4:])
	past := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(oldBlob, past, past))
	require.NoError(t, os.Chtimes(oldBlob+".meta", past, past))

	require.NoError(t, GCFileBucket(bucket, 24*time.Hour, logger))

	// Old blob and its meta should be gone.
	_, err := os.Stat(oldBlob)
	require.True(t, os.IsNotExist(err), "old blob should have been removed")
	_, err = os.Stat(oldBlob + ".meta")
	require.True(t, os.IsNotExist(err), "old meta should have been removed")

	// New blob should remain.
	newBlob := filepath.Join(bucket, newHash[0:2], newHash[2:4], newHash[4:])
	_, err = os.Stat(newBlob)
	require.NoError(t, err, "recent blob should not be removed")
}
