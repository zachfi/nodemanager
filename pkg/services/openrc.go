package services

import (
	"context"
	"log/slog"
	"regexp"

	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/nodemanager/pkg/common"
)

// OPENRC
type ServiceHandlerOpenRC struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *ServiceHandlerOpenRC) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	return common.SimpleRunCommand("/sbin/rc-update", "add", name)
}

func (h *ServiceHandlerOpenRC) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	return common.SimpleRunCommand("/sbin/rc-update", "del", name)
}

func (h *ServiceHandlerOpenRC) SetArguments(ctx context.Context, _, _ string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	return nil
}

func (h *ServiceHandlerOpenRC) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return common.SimpleRunCommand("/sbin/rc-service", name, "start")
}

func (h *ServiceHandlerOpenRC) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return common.SimpleRunCommand("/sbin/rc-service", name, "stop")
}

func (h *ServiceHandlerOpenRC) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return common.SimpleRunCommand("/sbin/rc-service", name, "restart")
}

func (h *ServiceHandlerOpenRC) Status(ctx context.Context, name string) (string, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()

	output, exit, err := common.RunCommand("/bin/rc-status", "sysinit", "-Cqf", "ini")
	if exit == 0 {
		return Running.String(), nil
	}

	re := regexp.MustCompile(`(\w+)\s+=\s+(\w+)`)
	m := re.FindAllStringSubmatch(output, 0)
	if m != nil {
		for _, mm := range m {
			if mm[1] == name {
				if mm[2] == "running" {
					return Running.String(), nil
				}
			}
		}
	}

	return Stopped.String(), err
}
