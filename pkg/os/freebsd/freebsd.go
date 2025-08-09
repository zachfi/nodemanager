package freebsd

import (
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	nodeFreeBSD "github.com/zachfi/nodemanager/pkg/nodes/freebsd"
	"github.com/zachfi/nodemanager/pkg/packages/pkgng"
	svcFreeBSD "github.com/zachfi/nodemanager/pkg/services/freebsd"
)

var _ handler.System = (*FreeBSD)(nil)

type FreeBSD struct {
	logger *slog.Logger
}

func New(logger *slog.Logger) handler.System {
	return &FreeBSD{logger: logger}
}

func (a *FreeBSD) Package() handler.PackageHandler {
	return &pkgng.Pkgng{}
}

func (a *FreeBSD) Exec() handler.ExecHandler {
	return &execs.ExecHandlerCommon{}
}

func (a *FreeBSD) File() handler.FileHandler {
	return &files.FileHandlerCommon{}
}

func (a *FreeBSD) Service() handler.ServiceHandler {
	return &svcFreeBSD.FreeBSD{}
}

func (a *FreeBSD) Node() handler.NodeHandler {
	return nodeFreeBSD.New(a.logger)
}
