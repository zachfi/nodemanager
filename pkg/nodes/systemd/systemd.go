// Package systemd implements the handler.System interface for systems using systemd.
package systemd

import (
	"context"
	"log/slog"
	"os"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

var _ handler.NodeHandler = (*Systemd)(nil)

var tracer = otel.Tracer("nodes/systemd")

type Systemd struct {
	logger       *slog.Logger
	infoResolver handler.UnameInfoResolver
}

func New(logger *slog.Logger) handler.NodeHandler {
	return &Systemd{
		logger:       logger.With("node", "systemd"),
		infoResolver: handler.UnameInfoResolver{},
	}
}

func (h *Systemd) Reboot(ctx context.Context) {
	_, span := tracer.Start(ctx, "Reboot")
	defer span.End()

	err := execs.SimpleRunCommand("/usr/sbin/systemctl", "reboot")
	if err != nil {
		h.logger.Error("failed to call reboot", "err", err)
	}
}

func (h *Systemd) Upgrade(ctx context.Context) error {
	// TODO: consider firmware upgrades

	// Systemd does not have an Upgrade implementation.  Upgrades are handled through the package manager.
	return nil
}

func (h *Systemd) Hostname() (string, error) {
	return os.Hostname()
}

func (h *Systemd) Info(ctx context.Context) *handler.SysInfo {
	return h.infoResolver.Info(ctx)
}
