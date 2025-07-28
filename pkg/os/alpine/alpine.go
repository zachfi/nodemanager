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
}

func New(logger *slog.Logger) handler.System {
	return &AlpineLinux{logger: logger}
}

func (a *AlpineLinux) Package() handler.PackageHandler {
	return &apk.Apk{}
}

func (a *AlpineLinux) Exec() handler.ExecHandler {
	return &execs.ExecHandlerCommon{}
}

func (a *AlpineLinux) File() handler.FileHandler {
	return &files.FileHandlerCommon{}
}

func (a *AlpineLinux) Service() handler.ServiceHandler {
	return &openrc.OpenRC{}
}

func (a *AlpineLinux) Node() handler.NodeHandler {
	return alpine.New(a.logger)
}
