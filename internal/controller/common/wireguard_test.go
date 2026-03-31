package common

import (
	"context"
	"encoding/base64"
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

func TestGenerateWireGuardKeyPair(t *testing.T) {
	privKey, pubKey, err := generateWireGuardKeyPair()
	require.NoError(t, err)

	// Both keys must be valid base64.
	privBytes, err := base64.StdEncoding.DecodeString(privKey)
	require.NoError(t, err, "private key must be valid base64")
	pubBytes, err := base64.StdEncoding.DecodeString(pubKey)
	require.NoError(t, err, "public key must be valid base64")

	// Curve25519 keys are 32 bytes.
	require.Len(t, privBytes, 32, "private key must be 32 bytes")
	require.Len(t, pubBytes, 32, "public key must be 32 bytes")

	// Two calls must produce different keys.
	privKey2, pubKey2, err := generateWireGuardKeyPair()
	require.NoError(t, err)
	require.NotEqual(t, privKey, privKey2)
	require.NotEqual(t, pubKey, pubKey2)
}

func TestMergeBootstrappedWireGuardKey(t *testing.T) {
	live := []commonv1.WireGuardInterface{
		{Name: "wg0", PublicKey: "livekey==", ListenPort: 51820},
	}

	t.Run("adds entry when interface not in live list", func(t *testing.T) {
		result := mergeBootstrappedWireGuardKey(nil, "wg0", "bootstrappedkey==")
		require.Len(t, result, 1)
		require.Equal(t, commonv1.WireGuardInterface{Name: "wg0", PublicKey: "bootstrappedkey=="}, result[0])
	})

	t.Run("skips when interface already in live list", func(t *testing.T) {
		result := mergeBootstrappedWireGuardKey(live, "wg0", "bootstrappedkey==")
		require.Len(t, result, 1)
		// Live entry preserved — includes listen port.
		require.Equal(t, commonv1.WireGuardInterface{Name: "wg0", PublicKey: "livekey==", ListenPort: 51820}, result[0])
	})

	t.Run("adds different interface alongside live ones", func(t *testing.T) {
		result := mergeBootstrappedWireGuardKey(live, "wg1", "wg1key==")
		require.Len(t, result, 2)
		require.Equal(t, "wg0", result[0].Name)
		require.Equal(t, "wg1", result[1].Name)
	})

	t.Run("no-op when pubKey is empty", func(t *testing.T) {
		result := mergeBootstrappedWireGuardKey(live, "wg1", "")
		require.Len(t, result, 1)
	})
}

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
