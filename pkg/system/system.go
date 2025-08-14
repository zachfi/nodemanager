package system

import (
	"context"
	"errors"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/common/info"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/os/alpine"
	"github.com/zachfi/nodemanager/pkg/os/arch"
	"github.com/zachfi/nodemanager/pkg/os/freebsd"
)

var ErrSystemNotFound = errors.New("not found for system")

func New(ctx context.Context, logger *slog.Logger) (handler.System, error) {
	resolver := info.NewInfoResolver()

	switch OSIDFromString(resolver.Info(ctx).OS.ID) {
	case Arch:
		return arch.New(logger), nil
	case Alpine:
		return alpine.New(logger), nil
	case FreeBSD:
		return freebsd.New(logger), nil
	}

	return nil, ErrSystemNotFound
}

type OSID int64

const (
	UnhandledOsID OSID = iota
	Arch
	Alpine
	FreeBSD
)

// String returns the string representation of the OSID
func (o OSID) String() string {
	switch o {
	case Arch:
		return "arch"
	case Alpine:
		return "alpine"
	case FreeBSD:
		return "freebsd"
	}
	return "unhandled"
}

// OSIDFromString converts an OS ID string to an OSID type
func OSIDFromString(osid string) OSID {
	switch osid {
	case "arch", "archarm":
		return Arch
	case "alpine":
		return Alpine
	case "freebsd":
		return FreeBSD
	default:
		return UnhandledOsID
	}
}
