package common

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type PackageHandler interface {
	Install(context.Context, string) error
	Remove(context.Context, string) error
	List(context.Context) ([]string, error)
}

func GetPackageHandler(ctx context.Context, tracer trace.Tracer) (PackageHandler, error) {
	_, span := tracer.Start(ctx, "GetPackageHandler")
	defer span.End()

	switch GetSystemInfo().OS.ID {
	case "arch":
		return &PackageHandler_Archlinux{tracer: tracer}, nil
	case "freebsd":
		return &PackageHandler_FreeBSD{tracer: tracer}, nil
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
}

func (h *PackageHandler_FreeBSD) Install(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Install")
	defer span.End()
	return simpleRunCommand("pkg", "install", "-qy", name)
}

func (h *PackageHandler_FreeBSD) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	return simpleRunCommand("pkg", "remove", "-qy", name)
}
func (h *PackageHandler_FreeBSD) List(ctx context.Context) ([]string, error) {
	_, span := h.tracer.Start(ctx, "List")
	defer span.End()
	output, _, err := runCommand("pkg", "query", "-a", "'%n'")
	if err != nil {
		return []string{}, err
	}

	packages := strings.Split(output, "\n")

	return packages, nil
}

// ARCH
type PackageHandler_Archlinux struct {
	tracer trace.Tracer
}

func (h *PackageHandler_Archlinux) Install(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Install")
	defer span.End()
	return simpleRunCommand("/usr/bin/pacman", "-Sy", "--needed", name)
}

func (h *PackageHandler_Archlinux) Remove(ctx context.Context, name string) error {
	_, span := h.tracer.Start(ctx, "Remove")
	defer span.End()
	return simpleRunCommand("/usr/bin/pacman", "-Rcsy", name)
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
