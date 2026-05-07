package poudriere

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
)

func Test_Bulk(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	_, err := NewBulk(logger, &execs.ExecHandlerCommon{})
	require.NoError(t, err)
}

func TestBulkBuildArgs(t *testing.T) {
	cases := []struct {
		name     string
		jail     string
		tree     string
		ports    []string
		wantArgs []string
	}{
		{
			name:     "single port",
			jail:     "14amd64",
			tree:     "default",
			ports:    []string{"net/curl"},
			wantArgs: []string{"bulk", "-p", "default", "-j", "14amd64", "-J", "2", "net/curl"},
		},
		{
			name:     "multiple ports each as separate arg",
			jail:     "14amd64",
			tree:     "personal",
			ports:    []string{"net/curl", "sysutils/htop", "shells/zsh"},
			wantArgs: []string{"bulk", "-p", "personal", "-j", "14amd64", "-J", "2", "net/curl", "sysutils/htop", "shells/zsh"},
		},
		{
			name:     "empty ports list still emits the build flags",
			jail:     "14amd64",
			tree:     "default",
			ports:    nil,
			wantArgs: []string{"bulk", "-p", "default", "-j", "14amd64", "-J", "2"},
		},
		{
			name:     "uppercase -J for parallelism, lowercase -j for jail (regression)",
			jail:     "13arm64",
			tree:     "default",
			ports:    []string{"www/nginx"},
			wantArgs: []string{"bulk", "-p", "default", "-j", "13arm64", "-J", "2", "www/nginx"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &handler.MockExecHandler{}
			b, err := NewBulk(slog.Default(), mock)
			require.NoError(t, err)

			require.NoError(t, b.Build(context.Background(), tc.jail, tc.tree, tc.ports))

			calls, ok := mock.Recorder[poudriere]
			require.True(t, ok, "expected a recorded call to %s", poudriere)
			require.Len(t, calls, 1, "expected exactly one poudriere invocation")
			require.Equal(t, tc.wantArgs, calls[0])
		})
	}
}

func TestBulkSync(t *testing.T) {
	mock := &handler.MockExecHandler{}
	b, err := NewBulk(slog.Default(), mock)
	require.NoError(t, err)

	require.NoError(t, b.Sync(context.Background()))

	calls, ok := mock.Recorder[portshaker]
	require.True(t, ok, "expected a recorded call to %s", portshaker)
	require.Len(t, calls, 1)
	require.Equal(t, []string{"-v"}, calls[0])
}
