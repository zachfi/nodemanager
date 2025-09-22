package arch

import (
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	systemd_node "github.com/zachfi/nodemanager/pkg/nodes/systemd"
	"github.com/zachfi/nodemanager/pkg/packages/pacman"
	systemd_svc "github.com/zachfi/nodemanager/pkg/services/systemd"
)

var _ handler.System = (*ArchLinux)(nil)

type ArchLinux struct {
	logger *slog.Logger

	exec handler.ExecHandler
	f    handler.FileHandler
	node handler.NodeHandler
	pkg  handler.PackageHandler
	svc  handler.ServiceHandler
}

func New(logger *slog.Logger) handler.System {
	s := &ArchLinux{
		logger: logger,
		exec:   &execs.ExecHandlerCommon{},
		f:      files.New(logger, "root"),
	}
	s.pkg = pacman.New(logger, s.exec)
	s.svc = systemd_svc.New(logger, s.exec)
	s.node = systemd_node.New(logger, s.exec)

	return s
}

func (a *ArchLinux) Exec() handler.ExecHandler {
	return a.exec
}

func (a *ArchLinux) File() handler.FileHandler {
	return a.f
}

func (a *ArchLinux) Node() handler.NodeHandler {
	return a.node
}

func (a *ArchLinux) Package() handler.PackageHandler {
	return a.pkg
}

func (a *ArchLinux) Service() handler.ServiceHandler {
	return a.svc
}
