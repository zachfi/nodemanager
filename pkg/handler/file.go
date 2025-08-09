package handler

import "context"

type FileHandler interface {
	Chown(ctx context.Context, path, owner, group string) error
	SetMode(ctx context.Context, path, mode string) error
	WriteContentFile(ctx context.Context, path string, content []byte) error
	WriteTemplateFile(ctx context.Context, path, template string) error
}
