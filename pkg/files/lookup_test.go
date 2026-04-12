package files

import (
	"os/user"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupUID(t *testing.T) {
	current, err := user.Current()
	require.NoError(t, err)

	uid, err := lookupUID(current.Username)
	require.NoError(t, err)
	require.Equal(t, current.Uid, uid)
}

func TestLookupUID_Unknown(t *testing.T) {
	_, err := lookupUID("nonexistent_user_xyzzy_12345")
	require.Error(t, err)
}

func TestLookupGID(t *testing.T) {
	current, err := user.Current()
	require.NoError(t, err)

	// Look up the current user's primary group.
	g, err := user.LookupGroupId(current.Gid)
	require.NoError(t, err)

	gid, err := lookupGID(g.Name)
	require.NoError(t, err)
	require.Equal(t, current.Gid, gid)
}

func TestLookupGID_Unknown(t *testing.T) {
	_, err := lookupGID("nonexistent_group_xyzzy_12345")
	require.Error(t, err)
}

func TestLookupUsernameByUID(t *testing.T) {
	current, err := user.Current()
	require.NoError(t, err)

	name := lookupUsernameByUID(current.Uid)
	require.Equal(t, current.Username, name)
}

func TestLookupUsernameByUID_Unknown(t *testing.T) {
	name := lookupUsernameByUID("99999")
	// Best-effort: may return empty or a name if 99999 exists.
	// Just verify it doesn't panic.
	_ = name
}

func TestLookupGroupnameByGID(t *testing.T) {
	current, err := user.Current()
	require.NoError(t, err)

	g, err := user.LookupGroupId(current.Gid)
	require.NoError(t, err)

	name := lookupGroupnameByGID(current.Gid)
	require.Equal(t, g.Name, name)
}
