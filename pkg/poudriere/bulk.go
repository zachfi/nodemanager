package poudriere

import (
	"context"
	"log/slog"
	"strings"

	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("poudriere")

type Bulk interface {
	Build(ctx context.Context, jail string, tree string, ports []string) error
	Sync(ctx context.Context) error
}

var _ Bulk = (*PoudriereBulk)(nil)

type PoudriereBulk struct {
	logger *slog.Logger

	exec handler.ExecHandler
}

func NewBulk(logger *slog.Logger, exec handler.ExecHandler) (*PoudriereBulk, error) {
	return &PoudriereBulk{
		logger: logger,
		exec:   exec,
	}, nil
}

func (p *PoudriereBulk) Build(ctx context.Context, jail string, tree string, ports []string) error {
	return p.exec.SimpleRunCommand(ctx, poudriere, "bulk", "-p", tree, "-j", jail, "-j", "2", strings.Join(ports, ""))
}

func (p *PoudriereBulk) Sync(ctx context.Context) error {
	return p.exec.SimpleRunCommand(ctx, portshaker, "-v")
}
