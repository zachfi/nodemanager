package common

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"strconv"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type FileEnsure int64

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
	switch ensure {
	case "file", "": // default
		return File
	case "directory":
		return Directory
	case "symlink":
		return Symlink
	case "absent":
		return Absent
	default:
		return UnhandledFileEnsure
	}
}

type FileHandler interface {
	Chown(ctx context.Context, path, owner, group string) error
	SetMode(ctx context.Context, path, mode string) error
	WriteContentFile(ctx context.Context, path string, content []byte) error
	WriteTemplateFile(ctx context.Context, path, template string) error
}

func GetFileHandler(ctx context.Context, tracer trace.Tracer, log *slog.Logger, info SysInfoResolver) (FileHandler, error) {
	var err error
	_, span := tracer.Start(ctx, "GetFileHandler")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	logger := log.With("handler", "FileHandler")

	switch info.Info().OS.ID {
	case "arch", "archarm", "alpine", "freebsd":
		return &FileHandlerCommon{tracer: tracer, logger: logger}, nil
	}

	return &FileHandlerNull{}, fmt.Errorf("file handler not found for system")
}

type FileHandlerNull struct{}

func (h *FileHandlerNull) Chown(_ context.Context, _, _, _ string) error                { return nil }
func (h *FileHandlerNull) SetMode(_ context.Context, _, _ string) error                 { return nil }
func (h *FileHandlerNull) WriteContentFile(_ context.Context, _ string, _ []byte) error { return nil }
func (h *FileHandlerNull) WriteTemplateFile(_ context.Context, _, _ string) error       { return nil }

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

	// Render the template into bytes
	// b, err := render()
	// return h.WriteContentFile(ctx, path, b)

	return nil
}

func GetFileModeFromString(_ context.Context, mode string) (os.FileMode, error) {
	octalMode, err := strconv.ParseUint(mode, 0, 32)
	if err != nil {
		return os.FileMode(0), err
	}

	return os.FileMode(octalMode), nil
}
