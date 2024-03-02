package common

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type PackageHandler interface {
	Install(context.Context, string) error
	Remove(context.Context, string) error
	List(context.Context) ([]string, error)
}

func GetPackageHandler(ctx context.Context, tracer trace.Tracer, log logr.Logger) (PackageHandler, error) {
	var err error
	_, span := tracer.Start(ctx, "GetPackageHandler")
	defer span.End()
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	logger := log.WithName("PackageHandler")

	switch GetSystemInfo().OS.ID {
	case "arch":
		return &PackageHandler_Archlinux{tracer: tracer, logger: logger}, nil
	case "freebsd":
		return &PackageHandler_FreeBSD{tracer: tracer, logger: logger}, nil
	}

	return &PackageHandler_Null{}, fmt.Errorf("package handler not found for system")
}

type PackageHandler_Null struct{}

func (h *PackageHandler_Null) Install(_ context.Context, _ string) error { return nil }
func (h *PackageHandler_Null) Remove(_ context.Context, _ string) error  { return nil }
func (h *PackageHandler_Null) List(_ context.Context) ([]string, error)  { return []string{}, nil }

// FREEBSD
type PackageHandler_FreeBSD struct {
	tracer trace.Tracer
	logger logr.Logger
}

func (h *PackageHandler_FreeBSD) Install(ctx context.Context, name string) error {
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

	return simpleRunCommand("pkg", "install", "-qy", name)
}

func (h *PackageHandler_FreeBSD) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	span.SetAttributes(attribute.Bool("remove", true))
	h.logger.Info("removing package", "name", name)
	return simpleRunCommand("pkg", "remove", "-qy", name)
}

func (h *PackageHandler_FreeBSD) List(ctx context.Context) ([]string, error) {
	_, span := h.tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := runCommand("pkg", "query", "-a", "%n")
	if err != nil {
		return []string{}, err
	}

	packages := strings.Split(output, "\n")

	return packages, nil
}

// ARCH
type PackageHandler_Archlinux struct {
	tracer trace.Tracer
	logger logr.Logger
}

func (h *PackageHandler_Archlinux) Install(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Install")
	defer span.End()

	h.logger.Info("installing package", "name", name)
	return simpleRunCommand("/usr/bin/pacman", "-Sy", "--needed", "--noconfirm", name)
}

func (h *PackageHandler_Archlinux) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	return simpleRunCommand("/usr/bin/pacman", "-Rcs", "--noconfirm", name)
}

func (h *PackageHandler_Archlinux) List(ctx context.Context) ([]string, error) {
	_, span := h.tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := runCommand("/usr/bin/pacman", "-Q")
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
