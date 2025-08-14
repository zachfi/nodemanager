package apk

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

const apk = "/sbin/apk"

var _ handler.PackageHandler = (*Apk)(nil)

var tracer = otel.Tracer("packages/apk")

type Apk struct {
	exec   handler.ExecHandler
	logger *slog.Logger
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.PackageHandler {
	return &Apk{
		logger: logger,
		exec:   exec,
	}
}

func (h *Apk) Install(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Install")
	defer span.End()

	h.logger.Info("installing package", "name", name)
	return h.exec.SimpleRunCommand(ctx, apk, "add", name)
}

func (h *Apk) Remove(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Remove")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, apk, "del", name)
}

func (h *Apk) List(ctx context.Context) ([]string, error) {
	_, span := tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := h.exec.RunCommand(ctx, apk, "list", "-I")
	if err != nil {
		return []string{}, err
	}

	return h.matchPackageOutput(output), nil
}

func (h *Apk) UpgradeAll(ctx context.Context) error {
	_, span := tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	err := h.exec.SimpleRunCommand(ctx, apk, "update")
	if err != nil {
		return err
	}

	return h.exec.SimpleRunCommand(ctx, apk, "upgrade")
}

func (h *Apk) matchPackageOutput(output string) []string {
	re := regexp.MustCompile(`^(.+)-([^-]+)-r([^-]+) (\S+) \{(\S+)\} \((.+?)\) \[(\w+)\]$`)
	lines := strings.Split(output, "\n")

	var packages []string

	for _, l := range lines {
		m := re.FindAllStringSubmatch(l, -1)
		if m == nil {
			continue
		}

		for _, mm := range m {
			packages = append(packages, mm[1])
		}
	}

	return packages
}
