package execs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/pkg/errors"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel/trace"
)

var _ handler.ExecHandler = (*ExecHandlerCommon)(nil)

type ExecHandlerCommon struct {
	tracer trace.Tracer
}

func (h *ExecHandlerCommon) RunCommand(ctx context.Context, command string, arg ...string) (string, int, error) {
	_, span := h.tracer.Start(ctx, "RunCommand")
	defer span.End()

	return RunCommand(command, arg...)
}

func RunCommand(command string, arg ...string) (string, int, error) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(command, arg...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return stderr.String(), cmd.ProcessState.ExitCode(), fmt.Errorf("failed to execute %q %s: %w", command, arg, err)
	}

	return out.String(), cmd.ProcessState.ExitCode(), nil
}

func SimpleRunCommand(command string, arg ...string) error {
	out, _, err := RunCommand(command, arg...)
	if err != nil {
		return errors.Wrap(err, out)
	}
	return err
}
