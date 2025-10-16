package handler

import "context"

type ExecHandler interface {
	RunCommand(ctx context.Context, command string, arg ...string) (string, int, error)
	SimpleRunCommand(ctx context.Context, command string, arg ...string) error
}

var _ ExecHandler = (*MockExecHandler)(nil)

type MockExecHandler struct {
	Recorder    map[string][]string
	expectedErr error
	Status      int
}

func (h *MockExecHandler) RunCommand(ctx context.Context, command string, args ...string) (string, int, error) {
	if h.Recorder == nil {
		h.Recorder = make(map[string][]string)
	}

	h.Recorder[command] = args

	return "", h.Status, h.expectedErr
}

func (h *MockExecHandler) SimpleRunCommand(ctx context.Context, command string, args ...string) error {
	_, _, err := h.RunCommand(ctx, command, args...)
	return err
}
