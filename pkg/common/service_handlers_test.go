package common

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_GetServiceHandler(t *testing.T) {
	cases := []struct {
		info       *SysInfo
		resultType ServiceHandler
		err        error
	}{
		{
			info: &SysInfo{
				OS: OS{
					ID: "arch",
				},
			},
			resultType: &ServiceHandlerSystemd{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "archarm",
				},
			},
			resultType: &ServiceHandlerSystemd{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "freebsd",
				},
			},
			resultType: &ServiceHandlerFreeBSD{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "none",
				},
			},
			resultType: &ServiceHandlerNull{},
			err:        ErrSystemNotFound,
		},
	}

	var (
		ctx    = context.Background()
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	)

	for _, tc := range cases {
		p, err := GetServiceHandler(ctx, nil, logger, &MockInfoResolver{info: tc.info})
		if tc.err != nil {
			require.ErrorIs(t, tc.err, err)
		} else {
			require.NoError(t, err)
		}

		assert.IsType(t, tc.resultType, p)
	}
}
