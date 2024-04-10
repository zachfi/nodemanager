package services

import (
	"context"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/trace"
)

// SYSTEMD
type ServiceHandlerSystemd struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *ServiceHandlerSystemd) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	return common.SimpleRunCommand("/usr/bin/systemctl", "enable", name)
}

func (h *ServiceHandlerSystemd) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	return common.SimpleRunCommand("/usr/bin/systemctl", "disable", name)
}

func (h *ServiceHandlerSystemd) SetArguments(ctx context.Context, _, _ string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	return nil
}

func (h *ServiceHandlerSystemd) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return common.SimpleRunCommand("/usr/bin/systemctl", "start", name)
}

func (h *ServiceHandlerSystemd) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return common.SimpleRunCommand("/usr/bin/systemctl", "stop", name)
}

func (h *ServiceHandlerSystemd) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return common.SimpleRunCommand("/usr/bin/systemctl", "restart", name)
}

func (h *ServiceHandlerSystemd) Status(ctx context.Context, name string) (string, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := common.RunCommand("/usr/bin/systemctl", "is-active", "--quiet", name)
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}
