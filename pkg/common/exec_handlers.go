package common

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type ExecHandler interface {
	RunCommand(ctx context.Context, command string, arg ...string) (string, int, error)
}

func GetExecHandler(ctx context.Context, tracer trace.Tracer, info SysInfoResolver) (ExecHandler, error) {
	var err error

	if tracer != nil {
		_, span := tracer.Start(ctx, "GetExecHandler")
		defer span.End()
		defer func() {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		}()
	}

	switch info.Info().OS.ID {
	case "arch", "archarm", "freebsd":
		return &ExecHandlerCommon{tracer: tracer}, nil
	}

	return &ExecHandlerNull{}, ErrSystemNotFound
}

type ExecHandlerNull struct{}

func (h *ExecHandlerNull) RunCommand(_ context.Context, _ string, _ ...string) (string, int, error) {
	return "", 0, nil
}

type ExecHandlerCommon struct {
	tracer trace.Tracer
}

func (h *ExecHandlerCommon) RunCommand(ctx context.Context, command string, arg ...string) (string, int, error) {
	_, span := h.tracer.Start(ctx, "RunCommand")
	defer span.End()

	return runCommand(command, arg...)
}
