package systemd

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/services"
)

var (
	testUser = "testuser"
	testSvc  = "testservice"
)

func TestSystemd(t *testing.T) {
	var (
		logHandler = slog.NewTextHandler(os.Stdout, nil)
		logger     = slog.New(logHandler)
	)

	cases := []struct {
		ctx          context.Context
		expectedUser string
		svc          string
		enableArgs   []string
		disableArgs  []string
		startArgs    []string
		stopArgs     []string
		restartArgs  []string
		statusArgs   []string
		statusExit   int
	}{
		{
			ctx:         context.Background(),
			svc:         testSvc,
			enableArgs:  []string{"enable", testSvc},
			disableArgs: []string{"disable", testSvc},
			startArgs:   []string{"start", testSvc},
			stopArgs:    []string{"stop", testSvc},
			restartArgs: []string{"restart", testSvc},
			statusArgs:  []string{"is-active", "--quiet", testSvc},
		},
		{
			ctx:          context.WithValue(context.Background(), UserContextKey, testUser),
			expectedUser: testUser,
			svc:          testSvc,
			enableArgs:   []string{"--user", "-M", testUser + "@", "enable", testSvc},
			disableArgs:  []string{"--user", "-M", testUser + "@", "disable", testSvc},
			startArgs:    []string{"--user", "-M", testUser + "@", "start", testSvc},
			stopArgs:     []string{"--user", "-M", testUser + "@", "stop", testSvc},
			restartArgs:  []string{"--user", "-M", testUser + "@", "restart", testSvc},
			statusArgs:   []string{"--user", "-M", testUser + "@", "is-active", "--quiet", testSvc},
		},
	}

	for _, tc := range cases {
		// Create a fresh mock per case so the recorder index is predictable.
		// Status queue must cover every RunCommand call: Enable, Disable,
		// Start, Stop, Restart (all succeed → 0), then Status×2 (0=Running,
		// 1=Stopped).
		mock := &handler.MockExecHandler{Status: []int{0, 0, 0, 0, 0, 0, 1}}
		h := New(logger, mock)
		hh := h.(*Systemd).WithContext(tc.ctx)

		require.Equal(t, tc.expectedUser, hh.(*Systemd).user)

		calls := mock.Recorder[systemctl]

		require.NoError(t, hh.Enable(tc.ctx, tc.svc))
		require.Equal(t, tc.enableArgs, mock.Recorder[systemctl][len(calls)])
		calls = mock.Recorder[systemctl]

		require.NoError(t, hh.Disable(tc.ctx, tc.svc))
		require.Equal(t, tc.disableArgs, mock.Recorder[systemctl][len(calls)])
		calls = mock.Recorder[systemctl]

		require.NoError(t, hh.Start(tc.ctx, tc.svc))
		require.Equal(t, tc.startArgs, mock.Recorder[systemctl][len(calls)])
		calls = mock.Recorder[systemctl]

		require.NoError(t, hh.Stop(tc.ctx, tc.svc))
		require.Equal(t, tc.stopArgs, mock.Recorder[systemctl][len(calls)])
		calls = mock.Recorder[systemctl]

		require.NoError(t, hh.Restart(tc.ctx, tc.svc))
		require.Equal(t, tc.restartArgs, mock.Recorder[systemctl][len(calls)])
		calls = mock.Recorder[systemctl]

		// Status consumes from the Status queue: first call → 0 (Running).
		status, err := hh.Status(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.statusArgs, mock.Recorder[systemctl][len(calls)])
		require.Equal(t, services.Running, status)
		calls = mock.Recorder[systemctl]

		// Second call → 1 (Stopped).
		status, err = hh.Status(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.statusArgs, mock.Recorder[systemctl][len(calls)])
		require.Equal(t, services.Stopped, status)
	}
}
