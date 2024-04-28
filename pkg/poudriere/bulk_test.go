package poudriere

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func Test_Bulk(t *testing.T) {
	tracer := trace.NewNoopTracerProvider().Tracer("test")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	_, err := NewBulk(logger, tracer)
	require.NoError(t, err)
}
