package jail

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// canned jls -v --libxo=json output with two running jails.
const jlsTwo = `{"__version":"2","jail-information":{"jail":[` +
	`{"jid":2,"hostname":"web01","path":"/usr/local/nodemanager/jails/web01/root","name":"web01","state":"ACTIVE","cpusetid":3,"ipv4_addrs":["192.0.2.10"],"ipv6_addrs":[]},` +
	`{"jid":5,"hostname":"db01","path":"/usr/local/nodemanager/jails/db01/root","name":"db01","state":"ACTIVE","cpusetid":4,"ipv4_addrs":["192.0.2.20"],"ipv6_addrs":["2001:db8::20"]}` +
	`]}}`

// canned output with no jails running (empty array).
const jlsEmpty = `{"__version":"2","jail-information":{"jail":[]}}`

func TestListRunningJails(t *testing.T) {
	cases := []struct {
		name      string
		output    string
		wantLen   int
		wantNames []string
	}{
		{
			name:      "two jails running",
			output:    jlsTwo,
			wantLen:   2,
			wantNames: []string{"web01", "db01"},
		},
		{
			name:    "no jails running",
			output:  jlsEmpty,
			wantLen: 0,
		},
		{
			name:    "empty output",
			output:  "",
			wantLen: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := &handler.MockExecHandler{
				Status: []int{0},
				Output: []string{tc.output},
			}

			jails, err := listRunningJails(ctx, m)
			require.NoError(t, err)
			require.Len(t, jails, tc.wantLen)

			for i, name := range tc.wantNames {
				require.Equal(t, name, jails[i].Name)
			}
		})
	}
}

func TestListRunningJails_ParsedFields(t *testing.T) {
	ctx := context.Background()
	m := &handler.MockExecHandler{
		Status: []int{0},
		Output: []string{jlsTwo},
	}

	jails, err := listRunningJails(ctx, m)
	require.NoError(t, err)
	require.Len(t, jails, 2)

	web := jails[0]
	require.Equal(t, 2, web.ID)
	require.Equal(t, "web01", web.Hostname)
	require.Equal(t, "/usr/local/nodemanager/jails/web01/root", web.Path)
	require.Equal(t, "ACTIVE", web.State)
	require.Equal(t, []string{"192.0.2.10"}, web.IPv4Addrs)
	require.Empty(t, web.IPv6Addrs)

	db := jails[1]
	require.Equal(t, []string{"2001:db8::20"}, db.IPv6Addrs)
}

func TestIsJailRunning(t *testing.T) {
	cases := []struct {
		name       string
		jailName   string
		output     string
		wantResult bool
	}{
		{
			name:       "jail is running",
			jailName:   "web01",
			output:     jlsTwo,
			wantResult: true,
		},
		{
			name:       "other jail is running, not this one",
			jailName:   "other",
			output:     jlsTwo,
			wantResult: false,
		},
		{
			name:       "no jails running",
			jailName:   "web01",
			output:     jlsEmpty,
			wantResult: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := &handler.MockExecHandler{
				Status: []int{0},
				Output: []string{tc.output},
			}

			running, err := isJailRunning(ctx, m, tc.jailName)
			require.NoError(t, err)
			require.Equal(t, tc.wantResult, running)

			// Verify jls was called with the right flags.
			require.Equal(t, []string{"-v", "--libxo=json"}, m.Recorder[jlsCmd][0])
		})
	}
}
