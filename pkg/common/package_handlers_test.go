package common

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_GetPackageHandler(t *testing.T) {
	cases := []struct {
		info       *SysInfo
		resultType PackageHandler
		err        error
	}{
		{
			info: &SysInfo{
				OS: OS{
					ID: "arch",
				},
			},
			resultType: &PackageHandlerArchlinux{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "archarm",
				},
			},
			resultType: &PackageHandlerArchlinux{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "freebsd",
				},
			},
			resultType: &PackageHandlerFreeBSD{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "none",
				},
			},
			resultType: &PackageHandlerNull{},
			err:        ErrSystemNotFound,
		},
	}

	var (
		ctx    = context.Background()
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	)

	for _, tc := range cases {
		p, err := GetPackageHandler(ctx, nil, logger, &MockInfoResolver{info: tc.info})
		if tc.err != nil {
			require.ErrorIs(t, tc.err, err)
		} else {
			require.NoError(t, err)
		}

		assert.IsType(t, tc.resultType, p)
	}
}
