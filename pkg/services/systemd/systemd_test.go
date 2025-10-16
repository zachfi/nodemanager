package systemd

import (
	"context"
	"log/slog"
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
		m          handler.ExecHandler = &handler.MockExecHandler{}
		logHandler                     = slog.NewTextHandler(t.Output(), nil)
		logger                         = slog.New(logHandler)
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
		h := New(logger, m)
		hh := h.(*Systemd).WithContext(tc.ctx)

		require.Equal(t, tc.expectedUser, hh.(*Systemd).user)

		err := hh.Enable(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.enableArgs, m.(*handler.MockExecHandler).Recorder[systemctl])

		err = hh.Disable(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.disableArgs, m.(*handler.MockExecHandler).Recorder[systemctl])

		err = hh.Start(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.startArgs, m.(*handler.MockExecHandler).Recorder[systemctl])

		err = hh.Stop(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.stopArgs, m.(*handler.MockExecHandler).Recorder[systemctl])

		err = hh.Restart(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.restartArgs, m.(*handler.MockExecHandler).Recorder[systemctl])

		m.(*handler.MockExecHandler).Status = 0
		status, err := hh.Status(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.statusArgs, m.(*handler.MockExecHandler).Recorder[systemctl])
		require.Equal(t, services.Running, status)

		m.(*handler.MockExecHandler).Status = 1
		status, err = hh.Status(tc.ctx, tc.svc)
		require.NoError(t, err)
		require.Equal(t, tc.statusArgs, m.(*handler.MockExecHandler).Recorder[systemctl])
		require.Equal(t, services.Stopped, status)

	}
}
