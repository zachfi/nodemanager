// Package freebsd implements the handler.System interface for FreeBSD systems.
package freebsd

import (
	"context"
	"log/slog"
	"os"

	"github.com/zachfi/nodemanager/pkg/common/info"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

const (
	shutdown      = "/sbin/shutdown"
	freebsdUpdate = "/usr/sbin/freebsd-update"
)

var _ handler.NodeHandler = (*FreeBSD)(nil)

var tracer = otel.Tracer("nodes/freebsd")

type FreeBSD struct {
	logger *slog.Logger

	info handler.InfoResolver
	exec handler.ExecHandler
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.NodeHandler {
	return &FreeBSD{
		logger: logger.With("node", "freebsd"),

		info: info.NewInfoResolver(),
		exec: exec,
	}
}

func (h *FreeBSD) Reboot(ctx context.Context) {
	_, span := tracer.Start(ctx, "Reboot")
	defer span.End()

	err := h.exec.SimpleRunCommand(ctx, shutdown, "-r", "now")
	if err != nil {
		h.logger.Error("failed to call reboot", "err", err)
	}
}

func (h *FreeBSD) Upgrade(ctx context.Context) error {
	_, span := tracer.Start(ctx, "Upgrade")
	defer span.End()

	output, exit, err := h.exec.RunCommand(ctx, freebsdUpdate, "fetch")
	if err != nil {
		h.logger.Error("failed to run freebsd-udpate fetch", "err", err, "exit", exit, "output", output)
	}

	output, exit, err = h.exec.RunCommand(ctx, freebsdUpdate, "install")
	if exit == 2 {
		return nil // no updates to install
	} else if err != nil {
		h.logger.Error("failed to run freebsd-update install", "err", err, "exit", exit, "output", output)
	}

	return nil
}

func (h *FreeBSD) Hostname() (string, error) {
	return os.Hostname()
}

func (h *FreeBSD) Info(ctx context.Context) *handler.SysInfo {
	return h.info.Info(ctx)
}
