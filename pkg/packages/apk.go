package packages

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/nodemanager/pkg/common"
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
	output, _, err := common.RunCommand(apk, "list", "-I")
	if err != nil {
		return []string{}, err
	}

	return h.matchPackageOutput(output), nil
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

func (h *PackageHandlerAlpine) matchPackageOutput(output string) []string {
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
