package handler

import (
	"context"

	"github.com/zachfi/nodemanager/pkg/services"
)

type ServiceHandler interface {
	Enable(context.Context, string) error
	Disable(context.Context, string) error
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Restart(context.Context, string) error
	Status(context.Context, string) (services.ServiceStatus, error)
	SetArguments(context.Context, string, string) error
}
