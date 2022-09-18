package common

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

type FileHandler interface {
	Chown(path, owner, group string) error
	SetMode(path, group string) error
	WriteContentFile(path string, content []byte) error
	WriteTemplateFile(path, template string) error
}

func GetFileHandler() (FileHandler, error) {

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
	octalMode, err := strconv.ParseUint(mode, 0, 32)
	if err != nil {
		return err
	}

	err = os.Chmod(path, os.FileMode(octalMode))
	if err != nil {
		return err
	}

	return nil
}

func (h *FileHandler_Common) WriteContentFile(path string, bytes []byte) error { return nil }
func (h *FileHandler_Common) WriteTemplateFile(path, template string) error    { return nil }
