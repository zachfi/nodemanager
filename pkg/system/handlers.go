package system

import (
	"context"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/common"
	"github.com/zachfi/nodemanager/pkg/packages"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Handler interface {
	Reboot(context.Context)
	Upgrade(context.Context) error
}

func GetSystemHandler(ctx context.Context, tracer trace.Tracer, log *slog.Logger, info common.SysInfoResolver) (Handler, error) {
	var err error

	if tracer != nil {
		_, span := tracer.Start(ctx, "GetSystemHandler")
		defer span.End()
		defer func() {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		}()
	}

	logger := log.With("handler", "SystemHandler")

	switch info.Info().OS.ID {
	case "arch", "archarm":
		return &HandlerSystemd{tracer: tracer, logger: logger}, nil
	case "freebsd":
		return &HandlerFreeBSD{tracer: tracer, logger: logger}, nil
	}

	return &HandlerNull{}, common.ErrSystemNotFound
}

type HandlerNull struct{}

func (h *HandlerNull) Reboot(_ context.Context)        {}
func (h *HandlerNull) Upgrade(_ context.Context) error { return nil }

type HandlerFreeBSD struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *HandlerFreeBSD) Reboot(ctx context.Context) {
	_, span := h.tracer.Start(ctx, "Reboot")
	defer span.End()

	// /sbin/shutdown
	err := common.SimpleRunCommand("/sbin/shutdown", "-r", "now")
	if err != nil {
		h.logger.Error("failed to call reboot")
	}
}

func (h *HandlerFreeBSD) Upgrade(ctx context.Context) error {
	_, span := h.tracer.Start(ctx, "Upgrade")
	defer span.End()

	packageHandler, err := packages.GetPackageHandler(ctx, h.tracer, h.logger, &common.UnameInfoResolver{})
	if err != nil {
		return err
	}

	// Upgrade all packages
	err = packageHandler.UpgradeAll(ctx)
	if err != nil {
		return err
	}

	_, exit, err := common.RunCommand("/usr/sbin/freebsd-update", "fetch", "install")
	if exit == 2 {
		return nil // no updates to install
	} else if err != nil {
		h.logger.Error("failed to call freebsd-update fetch install")
	}

	return nil
}

type HandlerSystemd struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func (h *HandlerSystemd) Reboot(ctx context.Context) {
	_, span := h.tracer.Start(ctx, "Reboot")
	defer span.End()

	err := common.SimpleRunCommand("/usr/sbin/systemctl", "reboot")
	if err != nil {
		h.logger.Error("failed to call reboot")
	}
}

func (h *HandlerSystemd) Upgrade(ctx context.Context) error {
	_, span := h.tracer.Start(ctx, "Upgrade")
	defer span.End()

	packageHandler, err := packages.GetPackageHandler(ctx, h.tracer, h.logger, &common.UnameInfoResolver{})
	if err != nil {
		return err
	}

	// Upgrade all packages
	return packageHandler.UpgradeAll(ctx)
}
