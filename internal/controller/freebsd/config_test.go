package freebsd

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnabledMode_IsEnabled(t *testing.T) {
	cases := []struct {
		name     string
		mode     EnabledMode
		jailed   bool
		expected bool
	}{
		{"auto on host enables", EnabledAuto, false, true},
		{"auto in jail disables", EnabledAuto, true, false},
		{"true on host enables", EnabledTrue, false, true},
		{"true in jail forces enable", EnabledTrue, true, true},
		{"false on host forces disable", EnabledFalse, false, false},
		{"false in jail disables", EnabledFalse, true, false},
		{"empty falls back to auto on host", EnabledMode(""), false, true},
		{"empty falls back to auto in jail", EnabledMode(""), true, false},
		{"unknown value falls back to auto on host", EnabledMode("yes"), false, true},
		{"unknown value falls back to auto in jail", EnabledMode("yes"), true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.mode.IsEnabled(tc.jailed))
		})
	}
}

func TestControllerConfig_ApplyDeprecations(t *testing.T) {
	t.Run("AllowInJail false is a no-op", func(t *testing.T) {
		c := &ControllerConfig{
			AllowInJail: false,
			Jail:        JailConfig{Enabled: EnabledAuto},
			Poudriere:   PoudriereConfig{Enabled: EnabledAuto},
		}
		require.False(t, c.ApplyDeprecations())
		require.Equal(t, EnabledAuto, c.Jail.Enabled)
		require.Equal(t, EnabledAuto, c.Poudriere.Enabled)
	})

	t.Run("AllowInJail true forces both auto values to true", func(t *testing.T) {
		c := &ControllerConfig{
			AllowInJail: true,
			Jail:        JailConfig{Enabled: EnabledAuto},
			Poudriere:   PoudriereConfig{Enabled: EnabledAuto},
		}
		require.True(t, c.ApplyDeprecations())
		require.Equal(t, EnabledTrue, c.Jail.Enabled)
		require.Equal(t, EnabledTrue, c.Poudriere.Enabled)
	})

	t.Run("AllowInJail true does not override explicit per-controller flags", func(t *testing.T) {
		// Operator wants Jail off in this jail but Poudriere on; explicit
		// values must win over the deprecated AllowInJail.
		c := &ControllerConfig{
			AllowInJail: true,
			Jail:        JailConfig{Enabled: EnabledFalse},
			Poudriere:   PoudriereConfig{Enabled: EnabledTrue},
		}
		require.True(t, c.ApplyDeprecations())
		require.Equal(t, EnabledFalse, c.Jail.Enabled, "explicit false must not be overridden")
		require.Equal(t, EnabledTrue, c.Poudriere.Enabled)
	})
}

func TestRegisterFlagsAndApplyDefaults_Defaults(t *testing.T) {
	c := &ControllerConfig{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	c.RegisterFlagsAndApplyDefaults("freebsd", fs)
	require.NoError(t, fs.Parse(nil))

	require.Equal(t, EnabledAuto, c.Jail.Enabled, "jail.enabled defaults to auto")
	require.Equal(t, EnabledAuto, c.Poudriere.Enabled, "poudriere.enabled defaults to auto")
	require.False(t, c.AllowInJail, "allow-in-jail defaults to false")
}

func TestRegisterFlagsAndApplyDefaults_ParseExplicitValues(t *testing.T) {
	c := &ControllerConfig{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	c.RegisterFlagsAndApplyDefaults("freebsd", fs)
	require.NoError(t, fs.Parse([]string{
		"--freebsd.jail.enabled=false",
		"--freebsd.poudriere.enabled=true",
	}))

	require.Equal(t, EnabledFalse, c.Jail.Enabled)
	require.Equal(t, EnabledTrue, c.Poudriere.Enabled)
}

func TestRegisterFlagsAndApplyDefaults_RejectsUnknownEnabledValue(t *testing.T) {
	c := &ControllerConfig{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&nopWriter{}) // suppress flag's own usage-on-error output
	c.RegisterFlagsAndApplyDefaults("freebsd", fs)

	err := fs.Parse([]string{"--freebsd.jail.enabled=maybe"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid enabled mode maybe")
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
