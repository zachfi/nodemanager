package apk

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
)

const apk = "/sbin/apk"

var _ handler.PackageHandler = (*Apk)(nil)

type Apk struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *Apk) Install(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Install")
	defer span.End()

	h.logger.Info("installing package", "name", name)
	return execs.SimpleRunCommand(apk, "add", name)
}

func (h *Apk) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	return execs.SimpleRunCommand(apk, "del", name)
}

func (h *Apk) List(ctx context.Context) ([]string, error) {
	_, span := h.tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := execs.RunCommand(apk, "list", "-I")
	if err != nil {
		return []string{}, err
	}

	return h.matchPackageOutput(output), nil
}

func (h *Apk) UpgradeAll(ctx context.Context) error {
	_, span := h.tracer.Start(ctx, "UpgradeAll")
	defer span.End()

	err := execs.SimpleRunCommand(apk, "update")
	if err != nil {
		return err
	}

	return execs.SimpleRunCommand(apk, "upgrade")
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
