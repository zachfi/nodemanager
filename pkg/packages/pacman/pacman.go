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

func (h *Pacman) Install(ctx context.Context, name, version string) error {
	_, span := tracer.Start(ctx, "Install")
	defer span.End()

	pkg := name
	if version != "" {
		pkg = name + "=" + version
	}

	h.logger.Info("installing package", "name", name, "version", version)
	return h.exec.SimpleRunCommand(ctx, pacman, "-Sy", "--needed", "--noconfirm", pkg)
}

func (h *Pacman) Remove(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Remove")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, pacman, "-Rcs", "--noconfirm", name)
}

func (h *Pacman) List(ctx context.Context) (map[string]string, error) {
	_, span := tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := h.exec.RunCommand(ctx, pacman, "-Q")
	if err != nil {
		return nil, err
	}

	packages := make(map[string]string)

	reader := bytes.NewReader([]byte(output))
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("unknown output")
		}
		packages[parts[0]] = parts[1]
	}

	return packages, nil
}

func (h *Pacman) UpgradeAll(ctx context.Context) error {
	_, span := tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	return h.exec.SimpleRunCommand(ctx, pacman, "-Syu", "--noconfirm")
}
