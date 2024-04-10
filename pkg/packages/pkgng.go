package packages

import (
	"context"
	"log/slog"
	"strings"

	"github.com/pkg/errors"
	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var pkg = "/usr/sbin/pkg"

// FREEBSD
type PackageHandlerFreeBSD struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *PackageHandlerFreeBSD) Install(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Install")
	defer span.End()

	span.SetAttributes(attribute.String("name", name))

	pkgs, err := h.List(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list packages")
	}

	for _, p := range pkgs {
		if p == name {
			return nil
		}
	}

	span.SetAttributes(attribute.Bool("install", true))
	h.logger.Info("installing package", "name", name)

	return common.SimpleRunCommand(pkg, "install", "-qy", name)
}

func (h *PackageHandlerFreeBSD) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	span.SetAttributes(attribute.Bool("remove", true))
	h.logger.Info("removing package", "name", name)
	return common.SimpleRunCommand(pkg, "remove", "-qy", name)
}

func (h *PackageHandlerFreeBSD) List(ctx context.Context) ([]string, error) {
	_, span := h.tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := common.RunCommand(pkg, "query", "-a", "%n")
	if err != nil {
		return []string{}, err
	}

	packages := strings.Split(output, "\n")

	return packages, nil
}

func (h *PackageHandlerFreeBSD) UpgradeAll(ctx context.Context) error {
	_, span := h.tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	h.logger.Info("upgrading packages")

	return common.SimpleRunCommand(pkg, "upgrade", "-y")
}
