package common

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
)

type FileEnsure int64

const (
	UnhandledFileEnsure FileEnsure = iota
	File
	Directory
	Symlink
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
	}
	return "unhandled"
}

func FileEnsureFromString(ensure string) FileEnsure {
	switch ensure {
	case "unhandled":
		return UnhandledFileEnsure
	case "file":
		return File
	case "directory":
		return Directory
	case "symlink":
		return Symlink
	default:
		return UnhandledFileEnsure
	}
}

type FileHandler interface {
	Chown(ctx context.Context, path, owner, group string) error
	SetMode(ctx context.Context, path, group string) error
	WriteContentFile(ctx context.Context, path string, content []byte) error
	WriteTemplateFile(ctx context.Context, path, template string) error
}

func GetFileHandler(ctx context.Context, tracer trace.Tracer, log logr.Logger) (FileHandler, error) {
	_, span := tracer.Start(ctx, "GetFileHandler")
	defer span.End()

	logger := log.WithName("FileHandler")

	switch GetSystemInfo().OS.ID {
	case "arch", "freebsd":
		return &FileHandler_Common{tracer: tracer, logger: logger}, nil
	}

	return &FileHandler_Null{}, fmt.Errorf("file handler not found for system")
}

type FileHandler_Null struct{}

func (h *FileHandler_Null) Chown(_ context.Context, _, _, _ string) error                { return nil }
func (h *FileHandler_Null) SetMode(_ context.Context, _, _ string) error                 { return nil }
func (h *FileHandler_Null) WriteContentFile(_ context.Context, _ string, _ []byte) error { return nil }
func (h *FileHandler_Null) WriteTemplateFile(_ context.Context, _, _ string) error       { return nil }

type FileHandler_Common struct {
	tracer trace.Tracer
	logger logr.Logger
}

func (h *FileHandler_Common) Chown(ctx context.Context, path, owner, group string) error {
	_, span := h.tracer.Start(ctx, "Chown")
	defer span.End()

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

func (h *FileHandler_Common) SetMode(ctx context.Context, path, mode string) error {
	_, span := h.tracer.Start(ctx, "SetMode")
	defer span.End()

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

func (h *FileHandler_Common) WriteContentFile(ctx context.Context, path string, bytes []byte) error {
	_, span := h.tracer.Start(ctx, "WriteContentFile")
	defer span.End()

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

func (h *FileHandler_Common) WriteTemplateFile(ctx context.Context, path, template string) error {
	_, span := h.tracer.Start(ctx, "WriteTemplateFile")
	defer span.End()

	return nil
}

func GetFileModeFromString(_ context.Context, mode string) (os.FileMode, error) {
	octalMode, err := strconv.ParseUint(mode, 0, 32)
	if err != nil {
		return os.FileMode(0), err
	}

	return os.FileMode(octalMode), nil
}
