package zfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/handler"
)

func TestExists(t *testing.T) {
	cases := []struct {
		name       string
		dataset    string
		status     []int
		wantExists bool
		wantArgs   []string
	}{
		{
			name:       "not found",
			dataset:    "zroot/nope",
			status:     []int{1},
			wantExists: false,
			wantArgs:   []string{"list", "zroot/nope"},
		},
		{
			name:       "found",
			dataset:    "zroot/yep",
			status:     []int{0},
			wantExists: true,
			wantArgs:   []string{"list", "zroot/yep"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := &handler.MockExecHandler{Status: tc.status}
			z := NewZfsManager(m)

			exists, err := z.Exists(ctx, tc.dataset)
			require.NoError(t, err)
			require.Equal(t, tc.wantExists, exists)
			require.Equal(t, tc.wantArgs, m.Recorder[zfsCmd][0])
		})
	}
}

func TestCreateDataset(t *testing.T) {
	cases := []struct {
		name    string
		dataset string
		opts    []string
		want    []string
	}{
		{
			name:    "no options",
			dataset: "zroot/jails/classic",
			want:    []string{"create", "zroot/jails/classic"},
		},
		{
			name:    "with options",
			dataset: "zroot/nodemanager",
			opts:    []string{"mountpoint=/usr/local/nodemanager", "compression=lz4"},
			want:    []string{"create", "-o", "mountpoint=/usr/local/nodemanager", "-o", "compression=lz4", "zroot/nodemanager"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := &handler.MockExecHandler{Status: []int{0}}
			z := NewZfsManager(m).(*zfsManager)

			err := z.createDataset(ctx, tc.dataset, tc.opts...)
			require.NoError(t, err)
			require.Equal(t, tc.want, m.Recorder[zfsCmd][0])
		})
	}
}

func TestEnsure(t *testing.T) {
	cases := []struct {
		name      string
		dataset   string
		opts      []string
		status    []int
		wantCalls [][]string
	}{
		{
			name:    "already exists",
			dataset: "zroot/existing",
			status:  []int{0},
			wantCalls: [][]string{
				{"list", "zroot/existing"},
			},
		},
		{
			name:    "does not exist",
			dataset: "zroot/new",
			status:  []int{1, 0},
			wantCalls: [][]string{
				{"list", "zroot/new"},
				{"create", "zroot/new"},
			},
		},
		{
			name:    "does not exist with options",
			dataset: "zroot/nodemanager",
			opts:    []string{"mountpoint=/usr/local/nodemanager"},
			status:  []int{1, 0},
			wantCalls: [][]string{
				{"list", "zroot/nodemanager"},
				{"create", "-o", "mountpoint=/usr/local/nodemanager", "zroot/nodemanager"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := &handler.MockExecHandler{Status: tc.status}
			z := NewZfsManager(m)

			err := z.Ensure(ctx, tc.dataset, tc.opts...)
			require.NoError(t, err)
			require.Equal(t, tc.wantCalls, m.Recorder[zfsCmd])
		})
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	m := &handler.MockExecHandler{Status: []int{0}}
	z := NewZfsManager(m)

	err := z.Snapshot(ctx, "zroot/nodemanager/releases/14.2-RELEASE", "classic")
	require.NoError(t, err)
	require.Equal(t, []string{"snapshot", "zroot/nodemanager/releases/14.2-RELEASE@classic"}, m.Recorder[zfsCmd][0])
}

func TestClone(t *testing.T) {
	cases := []struct {
		name     string
		snapshot string
		target   string
		opts     []string
		want     []string
	}{
		{
			name:     "no options",
			snapshot: "zroot/nodemanager/releases/14.2-RELEASE@classic",
			target:   "zroot/nodemanager/jails/classic/root",
			want:     []string{"clone", "zroot/nodemanager/releases/14.2-RELEASE@classic", "zroot/nodemanager/jails/classic/root"},
		},
		{
			name:     "with mountpoint",
			snapshot: "zroot/nodemanager/releases/14.2-RELEASE@myjail",
			target:   "zroot/nodemanager/jails/myjail/root",
			opts:     []string{"mountpoint=/usr/local/nodemanager/jails/myjail/root"},
			want:     []string{"clone", "-o", "mountpoint=/usr/local/nodemanager/jails/myjail/root", "zroot/nodemanager/releases/14.2-RELEASE@myjail", "zroot/nodemanager/jails/myjail/root"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := &handler.MockExecHandler{Status: []int{0}}
			z := NewZfsManager(m)

			err := z.Clone(ctx, tc.snapshot, tc.target, tc.opts...)
			require.NoError(t, err)
			require.Equal(t, tc.want, m.Recorder[zfsCmd][0])
		})
	}
}

func TestDestroyDataset(t *testing.T) {
	ctx := context.Background()
	m := &handler.MockExecHandler{Status: []int{0, 0}}
	z := NewZfsManager(m)

	require.NoError(t, z.DestroyDataset(ctx, "zroot/nodemanager/jails/classic"))
	require.Equal(t, []string{"destroy", "zroot/nodemanager/jails/classic"}, m.Recorder[zfsCmd][0])

	require.NoError(t, z.DestroyDatasetRecursive(ctx, "zroot/nodemanager/jails/classic"))
	require.Equal(t, []string{"destroy", "-r", "-f", "zroot/nodemanager/jails/classic"}, m.Recorder[zfsCmd][1])
}
