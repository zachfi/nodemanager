package apk

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Apk_matchPackageOutput(t *testing.T) {
	content, err := os.ReadFile("tests/apk_list.txt")
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	h := &Apk{
		logger: logger,
	}
	results := h.matchPackageOutput(string(content))

	expected := []string{
		"alpine-baselayout", "alpine-baselayout-data", "alpine-keys", "apache2", "apache2-openrc", "apk-tools", "apr", "apr-util", "busybox", "busybox-binsh", "ca-certificates-bundle", "hiredis", "ifupdown-ng", "libc-utils", "libcap2", "libcrypto3", "libexpat", "libssl3", "libuuid", "musl", "musl-utils", "nginx", "nginx-openrc", "openrc", "pcre", "pcre2", "scanelf", "ssl_client", "zlib",
	}

	assert.EqualValues(t, expected, results)
}
