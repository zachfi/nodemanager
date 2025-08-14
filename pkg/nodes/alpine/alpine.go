// Package alpine provides a handler for Alpine Linux systems.
package alpine

import (
	"context"
	"log/slog"
	"os"

	"github.com/zachfi/nodemanager/pkg/common/info"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

var _ handler.NodeHandler = (*Alpine)(nil)

var tracer = otel.Tracer("nodes/alpine")

type Alpine struct {
	logger *slog.Logger

	info handler.InfoResolver
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.NodeHandler {
	return &Alpine{
		logger: logger.With("node", "alpine"),

		info: info.NewInfoResolver(),
	}
}

func (h *Alpine) Reboot(ctx context.Context) {
	// Alpine does not have a Reboot implementation.
}

func (h *Alpine) Upgrade(ctx context.Context) error {
	// Alpine does not have an Upgrade implementation.
	return nil
}

func (h *Alpine) Hostname() (string, error) {
	return os.Hostname()
}

func (h *Alpine) Info(ctx context.Context) *handler.SysInfo {
	return h.info.Info(ctx)
}
