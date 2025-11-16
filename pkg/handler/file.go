package handler

import "context"

type FileHandler interface {
	// Chown receives a path and the file owner and group which should be set on
	// the file. A boolean indicating if the owner or group was changed and an
	// error, if any, is returned.
	Chown(ctx context.Context, path, owner, group string) (bool, error)

	// SetMode receives a path and the file mode which should be set on the file.
	// A boolean indicating if the mode was changed and an error, if any, is
	// returned.
	SetMode(ctx context.Context, path, mode string) (bool, error)

	// WriteContentFile receives a path and the content which should be written.
	// A boolean indicating if the file was modified and an error, if any, are
	// returned.
	WriteContentFile(ctx context.Context, path string, content []byte) (bool, error)

	// Remove receives a path which should be removed.  A boolean indicating if
	// the file was removed and an error, if any, are returned.
	Remove(ctx context.Context, path string) (bool, error)
}
