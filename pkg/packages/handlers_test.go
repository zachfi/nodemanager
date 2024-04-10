package packages

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/common"
)

func Test_GetPackageHandler(t *testing.T) {
	cases := []struct {
		info       *common.SysInfo
		resultType PackageHandler
		err        error
	}{
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "arch",
				},
			},
			resultType: &PackageHandlerPacman{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "archarm",
				},
			},
			resultType: &PackageHandlerPacman{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "freebsd",
				},
			},
			resultType: &PackageHandlerFreeBSD{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "alpine",
				},
			},
			resultType: &PackageHandlerAlpine{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "none",
				},
			},
			resultType: &PackageHandlerNull{},
			err:        common.ErrSystemNotFound,
		},
	}

	var (
		ctx    = context.Background()
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	)

	for _, tc := range cases {
		p, err := GetPackageHandler(ctx, nil, logger, &common.MockInfoResolver{SysInfo: tc.info})
		if tc.err != nil {
			require.ErrorIs(t, tc.err, err)
		} else {
			require.NoError(t, err)
		}

		assert.IsType(t, tc.resultType, p)
	}
}
