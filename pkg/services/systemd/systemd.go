package systemd

import (
	"context"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/services"
	"go.opentelemetry.io/otel/trace"
)

var _ handler.ServiceHandler = &Systemd{}

type Systemd struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *Systemd) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	return execs.SimpleRunCommand("/usr/bin/systemctl", "enable", name)
}

func (h *Systemd) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	return execs.SimpleRunCommand("/usr/bin/systemctl", "disable", name)
}

func (h *Systemd) SetArguments(ctx context.Context, _, _ string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	return nil
}

func (h *Systemd) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return execs.SimpleRunCommand("/usr/bin/systemctl", "start", name)
}

func (h *Systemd) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return execs.SimpleRunCommand("/usr/bin/systemctl", "stop", name)
}

func (h *Systemd) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return execs.SimpleRunCommand("/usr/bin/systemctl", "restart", name)
}

func (h *Systemd) Status(ctx context.Context, name string) (services.ServiceStatus, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := execs.RunCommand("/usr/bin/systemctl", "is-active", "--quiet", name)
	if exit == 0 {
		return services.Running, nil
	}

	return services.Stopped, err
}
