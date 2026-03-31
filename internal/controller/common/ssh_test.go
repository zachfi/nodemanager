package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// cannedSSHFP mimics `ssh-keygen -r myhost` output with one key of each common type.
const cannedSSHFP = `myhost IN SSHFP 1 1 bba3f1514c9a748d916527032b7d7048149710da
myhost IN SSHFP 1 2 1d37db387b7e038a8dadc08e3a3a2d7164f8f5d08b8b596b53a2eb43719dce46
myhost IN SSHFP 3 1 26ab7a454d1b8afb4f52b2ff9b4daa4c4ab4ecfb
myhost IN SSHFP 3 2 5a8be586a92131b2600bd18ee28cf701594803804a1726721dcfc422dee70365
myhost IN SSHFP 4 1 ec4b2b7ca8b36cc0676fdcbba57e5624cd516e30
myhost IN SSHFP 4 2 a34ae2f3c47f2baff735acffe4a9244b5de0b24e8df20b383d83cf1e4e9cb9eb
`

func TestCollectSSHHostKeys(t *testing.T) {
	ctx := context.Background()

	t.Run("parses all SSHFP lines", func(t *testing.T) {
		exec := &handler.MockExecHandler{
			Status: []int{0},
			Output: []string{cannedSSHFP},
		}
		keys := collectSSHHostKeys(ctx, exec, "myhost")
		require.Len(t, keys, 6)

		require.Equal(t, commonv1.SSHHostKey{Algorithm: 1, FingerprintType: 1, Fingerprint: "bba3f1514c9a748d916527032b7d7048149710da"}, keys[0])
		require.Equal(t, commonv1.SSHHostKey{Algorithm: 1, FingerprintType: 2, Fingerprint: "1d37db387b7e038a8dadc08e3a3a2d7164f8f5d08b8b596b53a2eb43719dce46"}, keys[1])
		require.Equal(t, commonv1.SSHHostKey{Algorithm: 4, FingerprintType: 2, Fingerprint: "a34ae2f3c47f2baff735acffe4a9244b5de0b24e8df20b383d83cf1e4e9cb9eb"}, keys[5])

		// Verify ssh-keygen was called with the right args.
		require.Equal(t, []string{"-r", "myhost"}, exec.Recorder["ssh-keygen"][0])
	})

	t.Run("returns nil when ssh-keygen is unavailable", func(t *testing.T) {
		exec := &handler.MockExecHandler{
			Status: []int{127},
			Output: []string{""},
		}
		keys := collectSSHHostKeys(ctx, exec, "myhost")
		require.Nil(t, keys)
	})

	t.Run("returns nil on empty output", func(t *testing.T) {
		exec := &handler.MockExecHandler{
			Status: []int{0},
			Output: []string{""},
		}
		keys := collectSSHHostKeys(ctx, exec, "myhost")
		require.Nil(t, keys)
	})

	t.Run("skips malformed lines", func(t *testing.T) {
		exec := &handler.MockExecHandler{
			Status: []int{0},
			Output: []string{"myhost IN SSHFP 4 2 abc123\nbadline\nmyhost IN SSHFP 1 1 deadbeef\n"},
		}
		keys := collectSSHHostKeys(ctx, exec, "myhost")
		require.Len(t, keys, 2)
	})
}
