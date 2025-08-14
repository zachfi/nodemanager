package freebsd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var _ handler.ServiceHandler = &FreeBSD{}

var tracer = otel.Tracer("services/freebsd")

type FreeBSD struct {
	logger *slog.Logger

	exec handler.ExecHandler
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.ServiceHandler {
	return &FreeBSD{
		logger: logger,
		exec:   exec,
	}
}

func (h *FreeBSD) Enable(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Enable")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return h.exec.SimpleRunCommand(ctx, "sysrc", "-f", rcFile, name+"_enable=YES")
}

func (h *FreeBSD) Disable(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Disable")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return h.exec.SimpleRunCommand(ctx, "sysrc", "-f", rcFile, name+"_enable=NO")
}

func (h *FreeBSD) SetArguments(ctx context.Context, name string, args string) error {
	_, span := tracer.Start(ctx, "SetArguments")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return h.exec.SimpleRunCommand(ctx, "sysrc", "-f", rcFile, fmt.Sprintf("%s_args=%s", name, args))
}

func (h *FreeBSD) Start(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Start")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "service", name, "start")
}

func (h *FreeBSD) Stop(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Stop")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "service", name, "stop")
}

func (h *FreeBSD) Restart(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Restart")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "service", name, "restart")
}

func (h *FreeBSD) Status(ctx context.Context, name string) (services.ServiceStatus, error) {
	status := services.Stopped

	_, span := tracer.Start(ctx, "Status")
	defer func() {
		span.SetAttributes(attribute.String("status", status.String()))
		span.End()
	}()

	_, exit, err := h.exec.RunCommand(ctx, "service", name, "status")
	span.SetAttributes(
		attribute.String("status", services.Running.String()),
	)
	if exit == 0 {
		status = services.Running
	}

	return status, err
}
