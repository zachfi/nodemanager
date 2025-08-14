package openrc

import (
	"context"
	"log/slog"
	"regexp"

	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/services"
	"go.opentelemetry.io/otel"
)

var _ handler.ServiceHandler = &OpenRC{}

var tracer = otel.Tracer("services/openrc")

type OpenRC struct {
	exec   handler.ExecHandler
	logger *slog.Logger
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.ServiceHandler {
	return &OpenRC{
		logger: logger,
		exec:   exec,
	}
}

func (h *OpenRC) Enable(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Enable")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "/sbin/rc-update", "add", name)
}

func (h *OpenRC) Disable(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Disable")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "/sbin/rc-update", "del", name)
}

func (h *OpenRC) SetArguments(ctx context.Context, _, _ string) error {
	_, span := tracer.Start(ctx, "SetArguments")
	defer span.End()
	return nil
}

func (h *OpenRC) Start(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Start")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "/sbin/rc-service", name, "start")
}

func (h *OpenRC) Stop(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Stop")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "/sbin/rc-service", name, "stop")
}

func (h *OpenRC) Restart(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Restart")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, "/sbin/rc-service", name, "restart")
}

func (h *OpenRC) Status(ctx context.Context, name string) (services.ServiceStatus, error) {
	_, span := tracer.Start(ctx, "Status")
	defer span.End()

	output, exit, err := h.exec.RunCommand(ctx, "/bin/rc-status", "sysinit", "-Cqf", "ini")
	if exit == 0 {
		return services.Running, nil
	}

	re := regexp.MustCompile(`(\w+)\s+=\s+(\w+)`)
	m := re.FindAllStringSubmatch(output, -1)
	for _, mm := range m {
		if mm[1] == name {
			if mm[2] == "running" {
				return services.Running, nil
			}
		}
	}

	return services.Stopped, err
}
