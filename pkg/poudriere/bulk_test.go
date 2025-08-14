package poudriere

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/execs"
)

func Test_Bulk(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	_, err := NewBulk(logger, &execs.ExecHandlerCommon{})
	require.NoError(t, err)
}
