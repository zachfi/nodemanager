package handler

import "context"

type ExecHandler interface {
	RunCommand(ctx context.Context, command string, arg ...string) (string, int, error)
	SimpleRunCommand(ctx context.Context, command string, arg ...string) error
}
