package packages

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/trace"
)

// ALPINE
var apk = "/sbin/apk"

type PackageHandlerAlpine struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *PackageHandlerAlpine) Install(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Install")
	defer span.End()

	h.logger.Info("installing package", "name", name)
	return common.SimpleRunCommand(apk, "add", name)
}

func (h *PackageHandlerAlpine) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	return common.SimpleRunCommand(apk, "del", name)
}

func (h *PackageHandlerAlpine) List(ctx context.Context) ([]string, error) {
	_, span := h.tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := common.RunCommand(apk, "list")
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

func (h *PackageHandlerAlpine) UpgradeAll(ctx context.Context) error {
	_, span := h.tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	err := common.SimpleRunCommand(apk, "update")
	if err != nil {
		return err
	}

	return common.SimpleRunCommand(apk, "upgrade")
}
