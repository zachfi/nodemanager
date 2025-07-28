// Package freebsd implements teh handler.System interface for FreeBSD systems.
package freebsd

import (
	"context"
	"log/slog"
	"os"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

var _ handler.NodeHandler = (*FreeBSD)(nil)

var tracer = otel.Tracer("nodes/freebsd")

type FreeBSD struct {
	logger       *slog.Logger
	infoResolver handler.UnameInfoResolver
}

func New(logger *slog.Logger) handler.NodeHandler {
	return &FreeBSD{
		logger:       logger.With("node", "systemd"),
		infoResolver: handler.UnameInfoResolver{},
	}
}

func (h *FreeBSD) Reboot(ctx context.Context) {
	_, span := tracer.Start(ctx, "Reboot")
	defer span.End()

	err := execs.SimpleRunCommand("/sbin/shutdown", "-r", "now")
	if err != nil {
		h.logger.Error("failed to call reboot", "err", err)
	}
}

func (h *FreeBSD) Upgrade(ctx context.Context) error {
	_, span := tracer.Start(ctx, "Upgrade")
	defer span.End()

	output, exit, err := execs.RunCommand("/usr/sbin/freebsd-update", "fetch")
	if err != nil {
		h.logger.Error("failed to run freebsd-udpate fetch", "err", err, "exit", exit, "output", output)
	}

	output, exit, err = execs.RunCommand("/usr/sbin/freebsd-update", "install")
	if exit == 2 {
		return nil // no updates to install
	} else if err != nil {
		h.logger.Error("failed to run freebsd-udpate install", "err", err, "exit", exit, "output", output)
	}

	return nil
}

func (h *FreeBSD) Hostname() (string, error) {
	return os.Hostname()
}

func (h *FreeBSD) Info(ctx context.Context) *handler.SysInfo {
	return h.infoResolver.Info(ctx)
}
