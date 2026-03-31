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

	expected := map[string]string{
		"alpine-baselayout":      "3.4.3-r2",
		"alpine-baselayout-data": "3.4.3-r2",
		"alpine-keys":            "2.4-r1",
		"apache2":                "2.4.59-r0",
		"apache2-openrc":         "2.4.59-r0",
		"apk-tools":              "2.14.3-r1",
		"apr":                    "1.7.4-r0",
		"apr-util":               "1.6.3-r1",
		"busybox":                "1.36.1-r15",
		"busybox-binsh":          "1.36.1-r15",
		"ca-certificates-bundle": "20240226-r0",
		"hiredis":                "1.2.0-r0",
		"ifupdown-ng":            "0.12.1-r4",
		"libc-utils":             "0.7.2-r5",
		"libcap2":                "2.69-r1",
		"libcrypto3":             "3.1.4-r5",
		"libexpat":               "2.6.2-r0",
		"libssl3":                "3.1.4-r5",
		"libuuid":                "2.39.3-r0",
		"musl":                   "1.2.4_git20230717-r4",
		"musl-utils":             "1.2.4_git20230717-r4",
		"nginx":                  "1.24.0-r15",
		"nginx-openrc":           "1.24.0-r15",
		"openrc":                 "0.52.1-r2",
		"pcre":                   "8.45-r3",
		"pcre2":                  "10.42-r2",
		"scanelf":                "1.3.7-r2",
		"ssl_client":             "1.36.1-r15",
		"zlib":                   "1.3.1-r0",
	}

	assert.EqualValues(t, expected, results)
}
