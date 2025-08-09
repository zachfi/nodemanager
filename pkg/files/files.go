package files

import (
	"context"
	"log/slog"
	"os"
	"os/user"
	"strconv"

	"github.com/pkg/errors"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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

type FileHandlerCommon struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *FileHandlerCommon) Chown(ctx context.Context, path, owner, group string) error {
	var err error
	_, span := h.tracer.Start(ctx, "Chown")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

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

	err = os.Chown(path, uid, gid)
	if err != nil {
		return errors.Wrap(err, "failed to chown file")
	}

	return nil
}

func (h *FileHandlerCommon) SetMode(ctx context.Context, path, mode string) error {
	var err error
	_, span := h.tracer.Start(ctx, "SetMode")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	span.SetAttributes(attribute.String("path", path))
	span.SetAttributes(attribute.String("mode", mode))

	fileMode, err := GetFileModeFromString(ctx, mode)
	if err != nil {
		return err
	}

	err = os.Chmod(path, fileMode)
	if err != nil {
		return err
	}

	return nil
}

func (h *FileHandlerCommon) WriteContentFile(ctx context.Context, path string, bytes []byte) error {
	var err error
	_, span := h.tracer.Start(ctx, "WriteContentFile")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	span.SetAttributes(attribute.String("path", path))

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}

func (h *FileHandlerCommon) WriteTemplateFile(ctx context.Context, path, template string) error {
	var err error
	_, span := h.tracer.Start(ctx, "WriteTemplateFile")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	span.SetAttributes(attribute.String("path", path))

	return nil
}

func GetFileModeFromString(_ context.Context, mode string) (os.FileMode, error) {
	octalMode, err := strconv.ParseUint(mode, 0, 32)
	if err != nil {
		return os.FileMode(0), err
	}

	return os.FileMode(octalMode), nil
}
