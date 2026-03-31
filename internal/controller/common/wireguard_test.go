package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// cannedWGDump mimics `wg show all dump` output with two interfaces and one
// peer each. Peer lines have 9 tab-separated fields and should be ignored.
const cannedWGDump = "wg0\t(hidden)\tabc123publickey==\t51820\toff\n" +
	"wg0\tpeerkey==\t(none)\t0.0.0.0/0\t0\t0\t0\toff\n" +
	"wg1\t(hidden)\txyz789publickey==\t51821\toff\n"

func TestCollectWireGuardInterfaces(t *testing.T) {
	ctx := context.Background()

	t.Run("parses interface lines only", func(t *testing.T) {
		exec := &handler.MockExecHandler{
			Status: []int{0},
			Output: []string{cannedWGDump},
		}
		ifaces := collectWireGuardInterfaces(ctx, exec)
		require.Len(t, ifaces, 2)

		require.Equal(t, commonv1.WireGuardInterface{Name: "wg0", PublicKey: "abc123publickey==", ListenPort: 51820}, ifaces[0])
		require.Equal(t, commonv1.WireGuardInterface{Name: "wg1", PublicKey: "xyz789publickey==", ListenPort: 51821}, ifaces[1])

		require.Equal(t, []string{"show", "all", "dump"}, exec.Recorder["wg"][0])
	})

	t.Run("returns nil when wg is unavailable", func(t *testing.T) {
		exec := &handler.MockExecHandler{
			Status: []int{1},
			Output: []string{""},
		}
		require.Nil(t, collectWireGuardInterfaces(ctx, exec))
	})

	t.Run("returns nil when no interfaces", func(t *testing.T) {
		exec := &handler.MockExecHandler{
			Status: []int{0},
			Output: []string{""},
		}
		require.Nil(t, collectWireGuardInterfaces(ctx, exec))
	})
}
