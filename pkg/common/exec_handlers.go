package common

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type ExecHandler interface {
	RunCommand(ctx context.Context, command string, arg ...string) (string, int, error)
}

func GetExecHandler(ctx context.Context, tracer trace.Tracer) (ExecHandler, error) {
	var err error
	_, span := tracer.Start(ctx, "GetExecHandler")
	defer span.End()
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	switch GetSystemInfo().OS.ID {
	case "arch", "freebsd":
		return &ExecHandler_Common{tracer: tracer}, nil
	}

	return &ExecHandler_Null{}, fmt.Errorf("exec handler not found for system")
}

type ExecHandler_Null struct{}

func (h *ExecHandler_Null) RunCommand(_ context.Context, _ string, _ ...string) (string, int, error) {
	return "", 0, nil
}

type ExecHandler_Common struct {
	tracer trace.Tracer
}

func (h *ExecHandler_Common) RunCommand(ctx context.Context, command string, arg ...string) (string, int, error) {
	_, span := h.tracer.Start(ctx, "RunCommand")
	defer span.End()

	return runCommand(command, arg...)
}
