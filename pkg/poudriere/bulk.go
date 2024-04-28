package poudriere

import (
	"log/slog"
	"strings"

	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/trace"
)

type Bulk interface {
	Build(jail string, tree string, ports []string) error
	Sync() error
}

var _ Bulk = &PoudriereBulk{}

type PoudriereBulk struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func NewBulk(logger *slog.Logger, tracer trace.Tracer) (*PoudriereBulk, error) {
	return &PoudriereBulk{
		logger: logger,
		tracer: tracer,
	}, nil
}

func (p *PoudriereBulk) Build(jail string, tree string, ports []string) error {
	return common.SimpleRunCommand(poudriere, "bulk", "-p", tree, "-j", jail, "-j", "2", strings.Join(ports, ""))
}

func (p *PoudriereBulk) Sync() error {
	return common.SimpleRunCommand(portshaker, "-v")
}
