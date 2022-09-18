package common

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
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
	Chown(path, owner, group string) error
	SetMode(path, group string) error
	WriteContentFile(path string, content []byte) error
	WriteTemplateFile(path, template string) error
}

func GetFileHandler() (FileHandler, error) {
	switch OsReleaseID() {
	case "arch", "freebsd":
		return &FileHandler_Common{}, nil
	}

	return &FileHandler_Null{}, fmt.Errorf("file handler not available for system")
}

type FileHandler_Null struct{}

func (h *FileHandler_Null) Chown(_, _, _ string) error                { return nil }
func (h *FileHandler_Null) SetMode(_, _ string) error                 { return nil }
func (h *FileHandler_Null) WriteContentFile(_ string, _ []byte) error { return nil }
func (h *FileHandler_Null) WriteTemplateFile(_, _ string) error       { return nil }

type FileHandler_Common struct{}

func (h *FileHandler_Common) Chown(path, owner, group string) error {

	u, err := user.Lookup(owner)
	if err != nil {
		return err
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}

	g, err := user.LookupGroup(group)
	if err != nil {
		return err
	}

	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return err
	}

	err = os.Chown(path, uid, gid)
	if err != nil {
		return err
	}

	return nil
}

func (h *FileHandler_Common) SetMode(path, mode string) error {
	fileMode, err := GetFileModeFromString(mode)
	if err != nil {
		return err
	}

	err = os.Chmod(path, fileMode)
	if err != nil {
		return err
	}

	return nil
}

func (h *FileHandler_Common) WriteContentFile(path string, bytes []byte) error {
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

func (h *FileHandler_Common) WriteTemplateFile(path, template string) error { return nil }

func GetFileModeFromString(mode string) (os.FileMode, error) {
	octalMode, err := strconv.ParseUint(mode, 0, 32)
	if err != nil {
		return os.FileMode(0), err
	}

	return os.FileMode(octalMode), nil
}
