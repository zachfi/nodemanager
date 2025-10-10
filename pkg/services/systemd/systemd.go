package systemd

import (
	"context"
	"log/slog"

	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/services"
	"go.opentelemetry.io/otel"
)

const systemctl = "/usr/bin/systemctl"

var _ handler.ServiceHandler = &Systemd{}

var tracer = otel.Tracer("services/systemd")

type Systemd struct {
	exec   handler.ExecHandler
	logger *slog.Logger
	user   string
}

func New(logger *slog.Logger, exec handler.ExecHandler) handler.ServiceHandler {
	return &Systemd{
		logger: logger,
		exec:   exec,
	}
}

func (h *Systemd) Enable(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Enable")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, systemctl, "enable", name)
}

func (h *Systemd) Disable(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Disable")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, systemctl, "disable", name)
}

func (h *Systemd) SetArguments(ctx context.Context, _, _ string) error {
	_, span := tracer.Start(ctx, "SetArguments")
	defer span.End()
	return nil
}

func (h *Systemd) Start(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Start")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, systemctl, "start", name)
}

func (h *Systemd) Stop(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Stop")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, systemctl, "stop", name)
}

func (h *Systemd) Restart(ctx context.Context, name string) error {
	_, span := tracer.Start(ctx, "Restart")
	defer span.End()
	return h.exec.SimpleRunCommand(ctx, systemctl, "restart", name)
}

func (h *Systemd) Status(ctx context.Context, name string) (services.ServiceStatus, error) {
	_, span := tracer.Start(ctx, "Status")
	defer span.End()
	_, exit, err := h.exec.RunCommand(ctx, systemctl, "is-active", "--quiet", name)
	if exit == 0 {
		return services.Running, nil
	}

	return services.Stopped, err
}

// WithContext receives a context and checks if a user value is set, and
// returns a new handler with the user set on the struct.  This allows the
// systemd specific handler to manage user services when set on the CRD.
// NOTE: WithContext is not part of the ServiceHandler interface.  Passing the
// user on the context allows keeping the interface clean and allowing the
// specific feature of systemd.
func (h *Systemd) WithContext(ctx context.Context) handler.ServiceHandler {
	if user, ok := ctx.Value(UserContextKey).(string); ok && user != "" && user != h.user {
		return &Systemd{
			exec:   h.exec,
			logger: h.logger.With("user", user),
			user:   user,
		}
	}

	return h
}

type contextKey string

const UserContextKey contextKey = "user"
