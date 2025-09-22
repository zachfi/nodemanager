package freebsd

import (
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	freebsd_node "github.com/zachfi/nodemanager/pkg/nodes/freebsd"
	"github.com/zachfi/nodemanager/pkg/packages/pkgng"
	freebsd_svc "github.com/zachfi/nodemanager/pkg/services/freebsd"
)

var _ handler.System = (*FreeBSD)(nil)

type FreeBSD struct {
	logger *slog.Logger

	exec handler.ExecHandler
	f    handler.FileHandler
	node handler.NodeHandler
	pkg  handler.PackageHandler
	svc  handler.ServiceHandler
}

func New(logger *slog.Logger) handler.System {
	s := &FreeBSD{
		logger: logger,
		exec:   &execs.ExecHandlerCommon{},
		f:      files.New(logger, "root", "wheel"),
	}
	s.pkg = pkgng.New(logger, s.exec)
	s.svc = freebsd_svc.New(logger, s.exec)
	s.node = freebsd_node.New(logger, s.exec)

	return s
}

func (a *FreeBSD) Exec() handler.ExecHandler {
	return a.exec
}

func (a *FreeBSD) File() handler.FileHandler {
	return a.f
}

func (a *FreeBSD) Node() handler.NodeHandler {
	return a.node
}

func (a *FreeBSD) Package() handler.PackageHandler {
	return a.pkg
}

func (a *FreeBSD) Service() handler.ServiceHandler {
	return a.svc
}
