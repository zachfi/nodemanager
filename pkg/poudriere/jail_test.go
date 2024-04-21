package poudriere

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

/*
❯ poudriere jail -l
JAILNAME VERSION          ARCH  METHOD TIMESTAMP           PATH
pkg13-2  13.2-RELEASE-p11 amd64 http   2024-04-21 01:39:10 /usr/local/poudriere/jails/pkg13-2
pkg14-0  14.0-RELEASE-p6  amd64 http   2024-04-05 13:59:09 /usr/local/poudriere/jails/pkg14-0
*/

func Test_jailList(t *testing.T) {
	tracer := trace.NewNoopTracerProvider().Tracer("test")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	_, err := NewJail(logger, tracer)
	require.NoError(t, err)
}
