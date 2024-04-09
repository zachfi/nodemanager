package common

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type ServiceHandler interface {
	Enable(context.Context, string) error
	Disable(context.Context, string) error
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Restart(context.Context, string) error
	Status(context.Context, string) (string, error)
	SetArguments(context.Context, string, string) error
}

type ServiceStatus int64

const (
	UnknownServiceStatus ServiceStatus = iota
	Running
	Stopped
)

func (s ServiceStatus) String() string {
	switch s {
	case UnknownServiceStatus:
		return "unknown"
	case Running:
		return "running"
	case Stopped:
		return "stopped"
	}
	return "unknown"
}

func GetServiceHandler(ctx context.Context, tracer trace.Tracer, log *slog.Logger, info SysInfoResolver) (ServiceHandler, error) {
	var err error

	if tracer != nil {
		_, span := tracer.Start(ctx, "GetServiceHandler")
		defer span.End()
		defer func() {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		}()
	}

	logger := log.With("handler", "ServiceHandler")

	switch info.Info().OS.ID {
	case "arch", "archarm":
		return &ServiceHandlerSystemd{tracer: tracer, logger: logger}, nil
	case "freebsd":
		return &ServiceHandlerFreeBSD{tracer: tracer, logger: logger}, nil
	}

	return &ServiceHandlerNull{}, ErrSystemNotFound
}

type ServiceHandlerNull struct{}

func (h *ServiceHandlerNull) Enable(_ context.Context, _ string) error          { return nil }
func (h *ServiceHandlerNull) Disable(_ context.Context, _ string) error         { return nil }
func (h *ServiceHandlerNull) Start(_ context.Context, _ string) error           { return nil }
func (h *ServiceHandlerNull) Stop(_ context.Context, _ string) error            { return nil }
func (h *ServiceHandlerNull) Restart(_ context.Context, _ string) error         { return nil }
func (h *ServiceHandlerNull) SetArguments(_ context.Context, _, _ string) error { return nil }
func (h *ServiceHandlerNull) Status(_ context.Context, _ string) (string, error) {
	return UnknownServiceStatus.String(), nil
}

// FREEBSD
type ServiceHandlerFreeBSD struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *ServiceHandlerFreeBSD) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return simpleRunCommand("sysrc", "-f", rcFile, name+"_enable=YES")
}

func (h *ServiceHandlerFreeBSD) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return simpleRunCommand("sysrc", "-f", rcFile, name+"_enable=NO")
}

func (h *ServiceHandlerFreeBSD) SetArguments(ctx context.Context, name string, args string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	rcFile := fmt.Sprintf("/etc/rc.conf.d/%s", name)
	return simpleRunCommand("sysrc", "-f", rcFile, fmt.Sprintf("%s_args=%s", name, args))
}

func (h *ServiceHandlerFreeBSD) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return simpleRunCommand("service", name, "start")
}

func (h *ServiceHandlerFreeBSD) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return simpleRunCommand("service", name, "stop")
}

func (h *ServiceHandlerFreeBSD) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return simpleRunCommand("service", name, "restart")
}

func (h *ServiceHandlerFreeBSD) Status(ctx context.Context, name string) (string, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := runCommand("service", name, "status")
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}

// LINUX
type ServiceHandlerSystemd struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *ServiceHandlerSystemd) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "enable", name)
}

func (h *ServiceHandlerSystemd) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "disable", name)
}

func (h *ServiceHandlerSystemd) SetArguments(ctx context.Context, _, _ string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	return nil
}

func (h *ServiceHandlerSystemd) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "start", name)
}

func (h *ServiceHandlerSystemd) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "stop", name)
}

func (h *ServiceHandlerSystemd) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "restart", name)
}

func (h *ServiceHandlerSystemd) Status(ctx context.Context, name string) (string, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := runCommand("/usr/bin/systemctl", "is-active", "--quiet", name)
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}
