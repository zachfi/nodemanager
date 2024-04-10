package services

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/common"
)

func Test_GetServiceHandler(t *testing.T) {
	cases := []struct {
		info       *common.SysInfo
		resultType Handler
		err        error
	}{
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "arch",
				},
			},
			resultType: &ServiceHandlerSystemd{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "archarm",
				},
			},
			resultType: &ServiceHandlerSystemd{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "freebsd",
				},
			},
			resultType: &ServiceHandlerFreeBSD{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "alpine",
				},
			},
			resultType: &ServiceHandlerOpenRC{},
		},
		{
			info: &common.SysInfo{
				OS: common.OS{
					ID: "none",
				},
			},
			resultType: &ServiceHandlerNull{},
			err:        common.ErrSystemNotFound,
		},
	}

	var (
		ctx    = context.Background()
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	)

	for _, tc := range cases {
		p, err := GetServiceHandler(ctx, nil, logger, &common.MockInfoResolver{SysInfo: tc.info})
		if tc.err != nil {
			require.ErrorIs(t, tc.err, err)
		} else {
			require.NoError(t, err)
		}

		assert.IsType(t, tc.resultType, p)
	}
}
