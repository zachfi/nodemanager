package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/trace"
)

// FREEBSD
type ServiceHandlerFreeBSD struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *ServiceHandlerFreeBSD) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return common.SimpleRunCommand("sysrc", "-f", rcFile, name+"_enable=YES")
}

func (h *ServiceHandlerFreeBSD) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return common.SimpleRunCommand("sysrc", "-f", rcFile, name+"_enable=NO")
}

func (h *ServiceHandlerFreeBSD) SetArguments(ctx context.Context, name string, args string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return common.SimpleRunCommand("sysrc", "-f", rcFile, fmt.Sprintf("%s_args=%s", name, args))
}

func (h *ServiceHandlerFreeBSD) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return common.SimpleRunCommand("service", name, "start")
}

func (h *ServiceHandlerFreeBSD) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return common.SimpleRunCommand("service", name, "stop")
}

func (h *ServiceHandlerFreeBSD) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return common.SimpleRunCommand("service", name, "restart")
}

func (h *ServiceHandlerFreeBSD) Status(ctx context.Context, name string) (string, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := common.RunCommand("service", name, "status")
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}
