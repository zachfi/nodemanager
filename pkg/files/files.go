package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"os/user"
	"strconv"
	"syscall"

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

func (h *FileHandlerCommon) Chown(ctx context.Context, path, owner, group string) error {
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

	u, err := user.Lookup(owner)
	if err != nil {
		return errors.Wrap(err, "failed to lookup user")
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return errors.Wrap(err, "failed to convert uid string")
	}

	g, err := user.LookupGroup(group)
	if err != nil {
		return errors.Wrap(err, "failed to lookup group")
	}

	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return errors.Wrap(err, "failed to convert gid string")
	}

	// Check the current file owner and group.
	fileInfo, err := os.Stat(path)
	if err != nil {
		return errors.Wrap(err, "failed to stat file")
	}

	if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		currentUID := int(stat.Uid)
		currentGID := int(stat.Gid)

		if currentGID == gid && currentUID == uid {
			return nil
		}
	}

	span.AddEvent("ownership set")

	err = os.Chown(path, uid, gid)
	if err != nil {
		return errors.Wrap(err, "failed to chown file")
	}

	return nil
}

func (h *FileHandlerCommon) SetMode(ctx context.Context, path, mode string) error {
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

	fileMode, err := GetFileModeFromString(ctx, mode)
	if err != nil {
		return err
	}

	// Check the current mode
	fileInfo, err := os.Stat(path)
	if err != nil {
		return errors.Wrap(err, "failed to stat file")
	}

	// Make no change to the file if the current mode matches the desired mode
	currentMode := fileInfo.Mode()
	if fileMode == currentMode {
		return nil
	}

	span.AddEvent("mode set")

	err = os.Chmod(path, fileMode)
	if err != nil {
		return err
	}

	return nil
}

func (h *FileHandlerCommon) WriteContentFile(ctx context.Context, path string, data []byte) error {
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
	if !os.IsNotExist(err) {
		// If the file exists, but we failed to read it for other reason, return the error.
		return err
	}

	// Check if the incoming data matches what is already on disk
	if h.hash(ctx, fileBytes) == h.hash(ctx, data) {
		return nil
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	h.logger.Info("writing file", "path", path)
	_, err = f.Write(data)
	if err != nil {
		return err
	}

	return nil
}

func (h *FileHandlerCommon) Remove(ctx context.Context, path string) error {
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
		return nil
	}

	err = os.Remove(path) // set the error on the span
	return err
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
