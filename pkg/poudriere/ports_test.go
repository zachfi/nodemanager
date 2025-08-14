package poudriere

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/execs"
)

/*
❯ poudriere ports -l
PORTSTREE METHOD    TIMESTAMP           PATH
default   git+https 2023-10-18 21:38:22 /usr/local/poudriere/ports/default
devel     svn       2021-10-03 18:40:24 /usr/local/poudriere/ports/devel
personal  git+https 2022-09-15 23:42:46 /usr/local/poudriere/ports/personal
*/

func Test_portsList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	_, err := NewPorts(logger, &execs.ExecHandlerCommon{})
	require.NoError(t, err)
}
