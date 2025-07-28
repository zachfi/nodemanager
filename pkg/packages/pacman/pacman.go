package pacman

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel/trace"
)

const pacman = "/usr/bin/pacman"

var _ handler.PackageHandler = (*Pacman)(nil)

type Pacman struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *Pacman) Install(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Install")
	defer span.End()

	h.logger.Info("installing package", "name", name)
	return execs.SimpleRunCommand(pacman, "-Sy", "--needed", "--noconfirm", name)
}

func (h *Pacman) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	return execs.SimpleRunCommand(pacman, "-Rcs", "--noconfirm", name)
}

func (h *Pacman) List(ctx context.Context) ([]string, error) {
	_, span := h.tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := execs.RunCommand(pacman, "-Q")
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
	_, span := h.tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	return execs.SimpleRunCommand(pacman, "-Syu", "--noconfirm")
}
