package handler

import "context"

type ExecHandler interface {
	RunCommand(ctx context.Context, command string, arg ...string) (string, int, error)
	SimpleRunCommand(ctx context.Context, command string, arg ...string) error
	// RunCommandWithInput runs command with the given string written to its
	// stdin. This avoids temporary files for commands that read config from
	// stdin (e.g. pfctl -f -), which matters on write-limited media such as
	// SD cards.
	RunCommandWithInput(ctx context.Context, stdin string, command string, arg ...string) (string, int, error)
}

var _ ExecHandler = (*MockExecHandler)(nil)

type MockExecHandler struct {
	Recorder    map[string][][]string
	expectedErr error
	Status      []int
	// Output holds per-call stdout values consumed in order, like Status.
	// When exhausted, RunCommand returns "".
	Output []string
	// InputRecorder stores the stdin string passed to each RunCommandWithInput
	// call, keyed by command name. Multiple calls append in order.
	InputRecorder map[string][]string
}

func (h *MockExecHandler) RunCommand(ctx context.Context, command string, args ...string) (string, int, error) {
	if h.Recorder == nil {
		h.Recorder = make(map[string][][]string)
	}

	h.Recorder[command] = append(h.Recorder[command], args)

	status := 0
	if len(h.Status) > 0 {
		status, h.Status = h.Status[0], h.Status[1:]
	}

	output := ""
	if len(h.Output) > 0 {
		output, h.Output = h.Output[0], h.Output[1:]
	}

	return output, status, h.expectedErr
}

func (h *MockExecHandler) SimpleRunCommand(ctx context.Context, command string, args ...string) error {
	_, _, err := h.RunCommand(ctx, command, args...)
	return err
}

func (h *MockExecHandler) RunCommandWithInput(ctx context.Context, stdin string, command string, args ...string) (string, int, error) {
	if h.InputRecorder == nil {
		h.InputRecorder = make(map[string][]string)
	}
	h.InputRecorder[command] = append(h.InputRecorder[command], stdin)
	return h.RunCommand(ctx, command, args...)
}
