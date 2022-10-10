package common

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
)

type ServiceHandler interface {
	Enable(context.Context, string) error
	Disable(context.Context, string) error
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Restart(context.Context, string) error
	Status(context.Context, string) (string, error)
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

func GetServiceHandler(ctx context.Context, tracer trace.Tracer) (ServiceHandler, error) {
	ctx, span := tracer.Start(ctx, "GetServiceHandler")
	defer span.End()

	switch GetSystemInfo(ctx).OSRelease {
	case "arch":
		return &ServiceHandler_Systemd{tracer: tracer}, nil
	case "freebsd":
		return &ServiceHandler_FreeBSD{tracer: tracer}, nil
	}

	return &ServiceHandler_Null{}, fmt.Errorf("service handler not available for system")
}

type ServiceHandler_Null struct{}

func (h *ServiceHandler_Null) Enable(_ context.Context, _ string) error          { return nil }
func (h *ServiceHandler_Null) Disable(_ context.Context, _ string) error         { return nil }
func (h *ServiceHandler_Null) Start(_ context.Context, _ string) error           { return nil }
func (h *ServiceHandler_Null) Stop(_ context.Context, _ string) error            { return nil }
func (h *ServiceHandler_Null) Restart(_ context.Context, _ string) error         { return nil }
func (h *ServiceHandler_Null) SetArguments(_ context.Context, _, _ string) error { return nil }
func (h *ServiceHandler_Null) Status(_ context.Context, _ string) (string, error) {
	return UnknownServiceStatus.String(), nil
}

// FREEBSD
type ServiceHandler_FreeBSD struct {
	tracer trace.Tracer
}

func (h *ServiceHandler_FreeBSD) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	return simpleRunCommand("sysrc", name+"_enable=YES")
}

func (h *ServiceHandler_FreeBSD) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	return simpleRunCommand("sysrc", name+"_enable=NO")
}

func (h *ServiceHandler_FreeBSD) SetArguments(ctx context.Context, name string, args string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	return simpleRunCommand("sysrc", fmt.Sprintf("%s_args=%q", name, args))
}

func (h *ServiceHandler_FreeBSD) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return simpleRunCommand("service", name, "start")
}

func (h *ServiceHandler_FreeBSD) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return simpleRunCommand("service", name, "stop")
}

func (h *ServiceHandler_FreeBSD) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return simpleRunCommand("service", name, "restart")
}

func (h *ServiceHandler_FreeBSD) Status(ctx context.Context, name string) (string, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := runCommand("service", name, "status")
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}

// LINUX
type ServiceHandler_Systemd struct {
	tracer trace.Tracer
}

func (h *ServiceHandler_Systemd) Enable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Enable")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "enable", name)
}

func (h *ServiceHandler_Systemd) Disable(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Disable")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "disable", name)
}

func (h *ServiceHandler_Systemd) SetArguments(ctx context.Context, _, _ string) error {
	_, span := h.tracer.Start(ctx, "SetArguments")
	defer span.End()
	return nil
}

func (h *ServiceHandler_Systemd) Start(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Start")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "start", name)
}

func (h *ServiceHandler_Systemd) Stop(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Stop")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "stop", name)
}

func (h *ServiceHandler_Systemd) Restart(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Restart")
	defer span.End()
	return simpleRunCommand("/usr/bin/systemctl", "restart", name)
}

func (h *ServiceHandler_Systemd) Status(ctx context.Context, name string) (string, error) {
	_, span := h.tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := runCommand("/usr/bin/systemctl", "is-active", "--quiet", name)
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}
