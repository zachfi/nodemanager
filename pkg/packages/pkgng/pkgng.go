package pkgng

import (
	"context"
	"log/slog"
	"strings"

	"github.com/pkg/errors"
	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const pkg = "/usr/sbin/pkg"

var _ handler.PackageHandler = (*Pkgng)(nil)

var tracer = otel.Tracer("packages/pkgng")

type Pkgng struct {
	logger *slog.Logger
	exec   handler.ExecHandler
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.PackageHandler {
	return &Pkgng{
		logger: logger,
		exec:   exec,
	}
}

func (h *Pkgng) Install(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Install")
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

	return h.exec.SimpleRunCommand(ctx, pkg, "install", "-qy", name)
}

func (h *Pkgng) Remove(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Remove")
	defer span.End()
	span.SetAttributes(attribute.Bool("remove", true))
	h.logger.Info("removing package", "name", name)
	return h.exec.SimpleRunCommand(ctx, pkg, "remove", "-qy", name)
}

func (h *Pkgng) List(ctx context.Context) ([]string, error) {
	_, span := tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := h.exec.RunCommand(ctx, pkg, "query", "-a", "%n")
	if err != nil {
		return []string{}, err
	}

	packages := strings.Split(output, "\n")

	return packages, nil
}

func (h *Pkgng) UpgradeAll(ctx context.Context) error {
	_, span := tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	h.logger.Info("upgrading packages")

	return h.exec.SimpleRunCommand(ctx, pkg, "upgrade", "-y")
}
