package arch

import (
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	nodeSystemd "github.com/zachfi/nodemanager/pkg/nodes/systemd"
	"github.com/zachfi/nodemanager/pkg/packages/pacman"
	svcSystemd "github.com/zachfi/nodemanager/pkg/services/systemd"
)

var _ handler.System = (*ArchLinux)(nil)

type ArchLinux struct {
	logger *slog.Logger
}

func New(logger *slog.Logger) handler.System {
	return &ArchLinux{logger: logger}
}

func (a *ArchLinux) Package() handler.PackageHandler {
	return &pacman.Pacman{}
}

func (a *ArchLinux) Exec() handler.ExecHandler {
	return &execs.ExecHandlerCommon{}
}

func (a *ArchLinux) File() handler.FileHandler {
	return &files.FileHandlerCommon{}
}

func (a *ArchLinux) Service() handler.ServiceHandler {
	return &svcSystemd.Systemd{}
}

func (a *ArchLinux) Node() handler.NodeHandler {
	return nodeSystemd.New(a.logger)
}
