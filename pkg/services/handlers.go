package services

import (
	"context"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Handler interface {
	Enable(context.Context, string) error
	Disable(context.Context, string) error
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Restart(context.Context, string) error
	Status(context.Context, string) (Status, error)
	SetArguments(context.Context, string, string) error
}

type Status int64

const (
	UnknownServiceStatus Status = iota
	Running
	Stopped
)

var StatusByName map[string]Status = map[string]Status{
	"unknown": UnknownServiceStatus,
	"running": Running,
	"stopped": Stopped,
}

func (s Status) String() string {
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

func GetServiceHandler(ctx context.Context, tracer trace.Tracer, log *slog.Logger, info common.SysInfoResolver) (Handler, error) {
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
	case "alpine":
		return &ServiceHandlerOpenRC{tracer: tracer, logger: logger}, nil
	}

	return &ServiceHandlerNull{}, common.ErrSystemNotFound
}

type ServiceHandlerNull struct{}

func (h *ServiceHandlerNull) Enable(_ context.Context, _ string) error          { return nil }
func (h *ServiceHandlerNull) Disable(_ context.Context, _ string) error         { return nil }
func (h *ServiceHandlerNull) Start(_ context.Context, _ string) error           { return nil }
func (h *ServiceHandlerNull) Stop(_ context.Context, _ string) error            { return nil }
func (h *ServiceHandlerNull) Restart(_ context.Context, _ string) error         { return nil }
func (h *ServiceHandlerNull) SetArguments(_ context.Context, _, _ string) error { return nil }
func (h *ServiceHandlerNull) Status(_ context.Context, _ string) (Status, error) {
	return UnknownServiceStatus, nil
}
