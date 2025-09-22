package alpine

import (
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/nodes/alpine"
	"github.com/zachfi/nodemanager/pkg/packages/apk"
	"github.com/zachfi/nodemanager/pkg/services/openrc"
)

var _ handler.System = (*AlpineLinux)(nil)

type AlpineLinux struct {
	logger *slog.Logger

	exec handler.ExecHandler
	f    handler.FileHandler
	node handler.NodeHandler
	pkg  handler.PackageHandler
	svc  handler.ServiceHandler
}

func New(logger *slog.Logger) handler.System {
	s := &AlpineLinux{
		logger: logger,
		exec:   &execs.ExecHandlerCommon{},
		f:      files.New(logger, "root"),
	}
	s.pkg = apk.New(logger, s.exec)
	s.svc = openrc.New(logger, s.exec)
	s.node = alpine.New(logger, s.exec)

	return s
}

func (a *AlpineLinux) Exec() handler.ExecHandler {
	return a.exec
}

func (a *AlpineLinux) File() handler.FileHandler {
	return a.f
}

func (a *AlpineLinux) Node() handler.NodeHandler {
	return a.node
}

func (a *AlpineLinux) Package() handler.PackageHandler {
	return a.pkg
}

func (a *AlpineLinux) Service() handler.ServiceHandler {
	return a.svc
}
