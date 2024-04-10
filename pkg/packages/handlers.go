package packages

import (
	"context"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type PackageHandler interface {
	Install(context.Context, string) error
	Remove(context.Context, string) error
	List(context.Context) ([]string, error)
	UpgradeAll(context.Context) error
}

func GetPackageHandler(ctx context.Context, tracer trace.Tracer, log *slog.Logger, info common.SysInfoResolver) (PackageHandler, error) {
	var err error

	if tracer != nil {
		_, span := tracer.Start(ctx, "GetPackageHandler")
		defer span.End()
		defer func() {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		}()
	}

	logger := log.With("handler", "PackageHandler")

	switch info.Info().OS.ID {
	case "arch", "archarm":
		return &PackageHandlerPacman{tracer: tracer, logger: logger}, nil
	case "freebsd":
		return &PackageHandlerFreeBSD{tracer: tracer, logger: logger}, nil
	case "alpine":
		return &PackageHandlerAlpine{tracer: tracer, logger: logger}, nil
	}

	return &PackageHandlerNull{}, common.ErrSystemNotFound
}

type PackageHandlerNull struct{}

func (h *PackageHandlerNull) Install(_ context.Context, _ string) error { return nil }
func (h *PackageHandlerNull) Remove(_ context.Context, _ string) error  { return nil }
func (h *PackageHandlerNull) List(_ context.Context) ([]string, error)  { return []string{}, nil }
func (h *PackageHandlerNull) UpgradeAll(_ context.Context) error        { return nil }
