package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_GetExecHandler(t *testing.T) {
	cases := []struct {
		info       *SysInfo
		resultType ExecHandler
		err        error
	}{
		{
			info: &SysInfo{
				OS: OS{
					ID: "arch",
				},
			},
			resultType: &ExecHandlerCommon{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "archarm",
				},
			},
			resultType: &ExecHandlerCommon{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "freebsd",
				},
			},
			resultType: &ExecHandlerCommon{},
		},
		{
			info: &SysInfo{
				OS: OS{
					ID: "none",
				},
			},
			resultType: &ExecHandlerNull{},
			err:        ErrSystemNotFound,
		},
	}

	ctx := context.Background()

	for _, tc := range cases {
		p, err := GetExecHandler(ctx, nil, &MockInfoResolver{info: tc.info})
		if tc.err != nil {
			require.ErrorIs(t, tc.err, err)
		} else {
			require.NoError(t, err)
		}

		assert.IsType(t, tc.resultType, p)
	}
}
