package execs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/pkg/errors"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

var _ handler.ExecHandler = (*ExecHandlerCommon)(nil)

var tracer = otel.Tracer("execs/common")

type ExecHandlerCommon struct{}

func (h *ExecHandlerCommon) RunCommand(ctx context.Context, command string, arg ...string) (string, int, error) {
	_, span := tracer.Start(ctx, "RunCommand")
	defer span.End()

	var out bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, command, arg...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return stderr.String(), cmd.ProcessState.ExitCode(), fmt.Errorf("failed to execute %q %s: %w", command, arg, err)
	}

	return out.String(), cmd.ProcessState.ExitCode(), nil
}

func (h *ExecHandlerCommon) SimpleRunCommand(ctx context.Context, command string, arg ...string) error {
	out, _, err := h.RunCommand(ctx, command, arg...)
	if err != nil {
		return errors.Wrap(err, out)
	}
	return err
}
