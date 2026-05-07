package poudriere

import (
	"context"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/handler"
	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("poudriere")

// defaultParallelJobs is the value passed to `poudriere bulk -J`, controlling
// how many ports build concurrently inside the build jail. Two is a
// conservative default that keeps memory pressure low on hosts with limited
// RAM; tuning is a future PoudriereConfig flag.
const defaultParallelJobs = "2"

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

// Build invokes `poudriere bulk` to build the given list of port origins
// (e.g. "net/curl") in the named build jail and ports tree. Each port is
// passed as a separate argument; concatenating them with strings.Join would
// either be parsed as a single nonsense origin (with sep="") or fail when the
// shell sees an unquoted whitespace blob.
//
// poudriere(8) uses lowercase -j for the build jail and uppercase -J for the
// parallel-job count. Using -j twice is a footgun (last-wins overwrites the
// jail name with the parallelism number).
func (p *PoudriereBulk) Build(ctx context.Context, jail string, tree string, ports []string) error {
	args := make([]string, 0, 7+len(ports))
	args = append(args, "bulk", "-p", tree, "-j", jail, "-J", defaultParallelJobs)
	args = append(args, ports...)
	return p.exec.SimpleRunCommand(ctx, poudriere, args...)
}

func (p *PoudriereBulk) Sync(ctx context.Context) error {
	return p.exec.SimpleRunCommand(ctx, portshaker, "-v")
}
