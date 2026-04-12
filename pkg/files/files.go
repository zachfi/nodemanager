package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type FileEnsure int64

var EnsureByName map[string]FileEnsure = map[string]FileEnsure{
	"unhandled": UnhandledFileEnsure,
	"file":      File,
	"directory": Directory,
	"symlink":   Symlink,
	"absent":    Absent,
	"":          File, // Default to File if empty string
}

const (
	UnhandledFileEnsure FileEnsure = iota
	File
	Directory
	Symlink
	Absent
)

func (f FileEnsure) String() string {
	switch f {
	case UnhandledFileEnsure:
		return "unhandled"
	case File:
		return "file"
	case Directory:
		return "directory"
	case Symlink:
		return "symlink"
	case Absent:
		return "absent"
	}
	return "unhandled"
}

func FileEnsureFromString(ensure string) FileEnsure {
	if f, ok := EnsureByName[ensure]; ok {
		return f
	}
	return UnhandledFileEnsure
}

var _ handler.FileHandler = (*FileHandlerCommon)(nil)

var tracer = otel.Tracer("files/common")

type FileHandlerCommon struct {
	logger       *slog.Logger
	defaultOwner string
	defaultGroup string
}

func New(logger *slog.Logger, defaultOwner, defaultGroup string) handler.FileHandler {
	return &FileHandlerCommon{logger, defaultOwner, defaultGroup}
}

func (h *FileHandlerCommon) Chown(ctx context.Context, path, owner, group string) (bool, error) {
	var err error
	_, span := tracer.Start(ctx, "Chown")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	if owner == "" {
		owner = h.defaultOwner
	}

	if group == "" {
		group = h.defaultGroup
	}

	span.SetAttributes(
		attribute.String("path", path),
		attribute.String("owner", owner),
		attribute.String("group", group),
	)

	uidStr, err := lookupUID(owner)
	if err != nil {
		return false, errors.Wrap(err, "failed to lookup user")
	}

	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return false, errors.Wrap(err, "failed to convert uid string")
	}

	gidStr, err := lookupGID(group)
	if err != nil {
		return false, errors.Wrap(err, "failed to lookup group")
	}

	gid, err := strconv.Atoi(gidStr)
	if err != nil {
		return false, errors.Wrap(err, "failed to convert gid string")
	}

	// Check the current file owner and group.
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, errors.Wrap(err, "failed to stat file")
	}

	if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		currentUID := int(stat.Uid)
		currentGID := int(stat.Gid)

		if currentGID == gid && currentUID == uid {
			return false, nil
		}
	}

	err = os.Chown(path, uid, gid)
	if err != nil {
		return false, errors.Wrap(err, "failed to chown file")
	}

	span.AddEvent("ownership set")

	return true, nil
}

func (h *FileHandlerCommon) SetMode(ctx context.Context, path, mode string) (bool, error) {
	var err error
	_, span := tracer.Start(ctx, "SetMode")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	span.SetAttributes(
		attribute.String("path", path),
		attribute.String("mode", mode),
	)

	// Check the current mode
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, errors.Wrap(err, "failed to stat file")
	}

	desiredFileMode, err := GetFileModeFromString(ctx, mode)
	if err != nil {
		return false, err
	}

	// NOTE: we use os.ModePerm to extract only the permission bits for comparison.
	var (
		currentPerms = fileInfo.Mode() & os.ModePerm
		desiredPerms = desiredFileMode & os.ModePerm
	)

	// Make no change to the file if the current mode matches the desired mode
	if desiredPerms == currentPerms {
		return false, nil
	}

	h.logger.Debug(
		"mode differs",
		"current", currentPerms.String(),
		"desired", desiredPerms.String(),
		"path", path,
	)

	err = os.Chmod(path, desiredFileMode)
	if err != nil {
		return false, err
	}

	span.AddEvent("mode set")

	return true, nil
}

func (h *FileHandlerCommon) WriteContentFile(ctx context.Context, path string, data []byte) (bool, error) {
	var err error
	_, span := tracer.Start(ctx, "WriteContentFile")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	span.SetAttributes(attribute.String("path", path))

	// Read the current file if it exists
	fileBytes, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		// If the file exists, but we failed to read it for other reason, return the error.
		return false, err
	}

	// Check if the incoming data matches what is already on disk
	dataHash := h.hash(ctx, data)
	if h.hash(ctx, fileBytes) == dataHash {
		return false, nil
	}

	f, err := os.Create(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	// NOTE: the file has been truncated on the Create() above.

	h.logger.Info("writing file", "path", path, "hash", dataHash)
	_, err = f.Write(data)
	if err != nil {
		return true, err
	}

	return true, nil
}

func (h *FileHandlerCommon) Remove(ctx context.Context, path string) (bool, error) {
	var err error
	_, span := tracer.Start(ctx, "Remove")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		err = nil // already absent — desired state reached
		return false, nil
	}

	err = os.Remove(path) // set the error on the span
	return true, err
}

func (h *FileHandlerCommon) hash(ctx context.Context, data []byte) string {
	_, span := tracer.Start(ctx, "hash")
	defer span.End()

	b := sha256.Sum256(data)
	return hex.EncodeToString(b[:])
}

func GetFileModeFromString(_ context.Context, mode string) (os.FileMode, error) {
	octalMode, err := strconv.ParseUint(mode, 0, 32)
	if err != nil {
		return os.FileMode(0), err
	}

	return os.FileMode(octalMode), nil
}

// FileBucketMeta is the JSON sidecar stored alongside each blob in the filebucket.
// It captures enough information to fully restore the original file.
type FileBucketMeta struct {
	Path       string `json:"path"`
	BackedUpAt string `json:"backedUpAt"`
	// Mode is the octal permission string, e.g. "0644".
	Mode  string `json:"mode"`
	UID   uint32 `json:"uid"`
	GID   uint32 `json:"gid"`
	Owner string `json:"owner,omitempty"` // best-effort name lookup
	Group string `json:"group,omitempty"`
}

// SaveToFileBucket writes data to a content-addressed filebucket store rooted at
// bucketPath. The blob is stored at:
//
//	<bucketPath>/<hash[0:2]>/<hash[2:4]>/<hash[4:]>
//
// with a JSON sidecar at the same path with a ".meta" suffix. Bucket directories
// are created with mode 0700; blobs are written with mode 0600.
//
// The call is idempotent: if the blob already exists it is not rewritten.
// info must be the os.FileInfo of the original file (used to capture permissions).
// Returns the hex-encoded SHA256 hash of data.
func SaveToFileBucket(bucketPath, filePath string, data []byte, info os.FileInfo) (string, error) {
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])

	dir := filepath.Join(bucketPath, hash[0:2], hash[2:4])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("filebucket: create dir %s: %w", dir, err)
	}

	blobPath := filepath.Join(dir, hash[4:])

	// Idempotent: skip write if blob already exists.
	if _, err := os.Stat(blobPath); err == nil {
		return hash, nil
	}

	if err := os.WriteFile(blobPath, data, 0o600); err != nil {
		return "", fmt.Errorf("filebucket: write blob: %w", err)
	}

	meta := FileBucketMeta{
		Path:       filePath,
		BackedUpAt: time.Now().UTC().Format(time.RFC3339),
		Mode:       fmt.Sprintf("%04o", info.Mode().Perm()),
	}

	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		meta.UID = stat.Uid
		meta.GID = stat.Gid
		meta.Owner = lookupUsernameByUID(strconv.Itoa(int(stat.Uid)))
		meta.Group = lookupGroupnameByGID(strconv.Itoa(int(stat.Gid)))
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return hash, nil // blob is written; meta failure is non-fatal
	}
	_ = os.WriteFile(blobPath+".meta", metaBytes, 0o600)

	return hash, nil
}

// GCFileBucket removes blobs (and their .meta sidecars) from bucketPath whose
// modification time is older than maxAge. Skips entries that are not regular
// files and skips .meta files (they are removed together with their blob).
func GCFileBucket(bucketPath string, maxAge time.Duration, logger *slog.Logger) error {
	removed := 0
	err := filepath.WalkDir(bucketPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		// meta sidecars are handled alongside their blob
		if filepath.Ext(path) == ".meta" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if time.Since(info.ModTime()) < maxAge {
			return nil
		}

		_ = os.Remove(path)
		_ = os.Remove(path + ".meta")
		removed++
		return nil
	})

	if removed > 0 {
		logger.Info("filebucket GC complete", "removed", removed, "bucket", bucketPath)
	}

	return err
}
