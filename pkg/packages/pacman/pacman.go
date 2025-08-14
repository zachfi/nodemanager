package pacman

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

const pacman = "/usr/bin/pacman"

var _ handler.PackageHandler = (*Pacman)(nil)

var tracer = otel.Tracer("packages/pacman")

type Pacman struct {
	exec   handler.ExecHandler
	logger *slog.Logger
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.PackageHandler {
	return &Pacman{
		logger: logger,
		exec:   exec,
	}
}

func (h *Pacman) Install(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Install")
	defer span.End()

	h.logger.Info("installing package", "name", name)
	return h.exec.SimpleRunCommand(ctx, pacman, "-Sy", "--needed", "--noconfirm", name)
}

func (h *Pacman) Remove(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Remove")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, pacman, "-Rcs", "--noconfirm", name)
}

func (h *Pacman) List(ctx context.Context) ([]string, error) {
	_, span := tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := h.exec.RunCommand(ctx, pacman, "-Q")
	if err != nil {
		return []string{}, err
	}

	var packages []string

	reader := bytes.NewReader([]byte(output))
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return []string{}, fmt.Errorf("unknown output")
		}
		packages = append(packages, parts[0])
	}

	return packages, nil
}

func (h *Pacman) UpgradeAll(ctx context.Context) error {
	_, span := tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	return h.exec.SimpleRunCommand(ctx, pacman, "-Syu", "--noconfirm")
}
