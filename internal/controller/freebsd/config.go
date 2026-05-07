package freebsd

import "flag"

// EnabledMode is a tri-state flag value used by per-controller enablement
// flags.  "auto" means "enable on the host, disable inside a jail."  "true"
// and "false" force the value regardless of jail context.  This lets an
// operator opt a privileged jail in to running a specific controller
// (typically Poudriere on the build jail) without forcing the Jail
// reconciler on every other jail in the deployment.
type EnabledMode string

const (
	// EnabledAuto means "enable on the host, disable inside a jail".
	EnabledAuto EnabledMode = "auto"
	// EnabledTrue forces the controller on, even inside a jail.
	EnabledTrue EnabledMode = "true"
	// EnabledFalse forces the controller off, even on the host.
	EnabledFalse EnabledMode = "false"
)

// IsEnabled resolves the tri-state against the jailed-process indicator.
// Unrecognised values fall back to "auto" so a typo doesn't silently
// disable a controller.
func (m EnabledMode) IsEnabled(isJailed bool) bool {
	switch m {
	case EnabledTrue:
		return true
	case EnabledFalse:
		return false
	default:
		return !isJailed
	}
}

type ControllerConfig struct {
	Poudriere PoudriereConfig
	Jail      JailConfig

	// AllowInJail is deprecated; use --freebsd.{jail,poudriere}.enabled=true
	// instead.  When set, both reconcilers are forced on even inside a
	// jail.  Kept for backward compatibility with v0.13 deployments; emits
	// a startup warning when set.
	//
	// Deprecated: split into per-controller --freebsd.<name>.enabled flags
	// in v0.14.
	AllowInJail bool
}

func (c *ControllerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Poudriere.RegisterFlagsAndApplyDefaults(prefix+".poudriere", f)
	c.Jail.RegisterFlagsAndApplyDefaults(prefix+".jail", f)
	f.BoolVar(&c.AllowInJail, prefix+".allow-in-jail", false,
		"Deprecated: use --"+prefix+".jail.enabled=true and --"+prefix+".poudriere.enabled=true. "+
			"When true, forces both Jail and Poudriere reconcilers on even inside a jail.")
}

// ApplyDeprecations folds AllowInJail into the per-controller Enabled flags
// when the deprecated flag was set and the new flags are still at their
// defaults.  Returns true when a deprecated flag was acted on so the caller
// can log a warning.
func (c *ControllerConfig) ApplyDeprecations() bool {
	if !c.AllowInJail {
		return false
	}
	if c.Jail.Enabled == EnabledAuto {
		c.Jail.Enabled = EnabledTrue
	}
	if c.Poudriere.Enabled == EnabledAuto {
		c.Poudriere.Enabled = EnabledTrue
	}
	return true
}

type PoudriereConfig struct {
	// Enabled controls whether the Poudriere reconciler runs.  See
	// EnabledMode for the tri-state semantics.  The reconciler also
	// requires the host's ManagedNode to carry the
	// freebsd.nodemanager/poudriere label, so setting this to true on a
	// node without the label is a no-op.
	Enabled EnabledMode
}

func (c *PoudriereConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.Var(enabledModeFlag{&c.Enabled}, prefix+".enabled",
		`Whether the Poudriere reconciler runs.  "auto" enables it on the host and disables it inside a jail; "true"/"false" force the value.  Default "auto".`)
	if c.Enabled == "" {
		c.Enabled = EnabledAuto
	}
}

type JailConfig struct {
	JailDataPath string
	ZfsDataset   string
	Mirror       string
	// Enabled controls whether the Jail reconciler runs.  See EnabledMode
	// for the tri-state semantics.  Set to "false" inside a privileged
	// jail running the Poudriere reconciler so the Jail reconciler's
	// informers don't compete with ConfigSet for resources.
	Enabled EnabledMode
}

func (c *JailConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&c.JailDataPath, prefix+".data-path", "/usr/local/nodemanager", "The base path where jail data is stored.")
	f.StringVar(&c.ZfsDataset, prefix+".zfs-dataset", "zroot/nodemanager", "The ZFS dataset to use for jail storage.")
	f.StringVar(&c.Mirror, prefix+".mirror", "https://download.freebsd.org/releases", "The FreeBSD mirror base URL for downloading release archives.")
	f.Var(enabledModeFlag{&c.Enabled}, prefix+".enabled",
		`Whether the Jail reconciler runs.  "auto" enables it on the host and disables it inside a jail; "true"/"false" force the value.  Default "auto".`)
	if c.Enabled == "" {
		c.Enabled = EnabledAuto
	}
}

// enabledModeFlag adapts EnabledMode to flag.Value so a single string flag
// covers all three values.
type enabledModeFlag struct{ v *EnabledMode }

func (f enabledModeFlag) String() string {
	if f.v == nil || *f.v == "" {
		return string(EnabledAuto)
	}
	return string(*f.v)
}

func (f enabledModeFlag) Set(s string) error {
	switch EnabledMode(s) {
	case EnabledAuto, EnabledTrue, EnabledFalse:
		*f.v = EnabledMode(s)
		return nil
	default:
		return &enabledModeError{value: s}
	}
}

type enabledModeError struct{ value string }

func (e *enabledModeError) Error() string {
	return "invalid enabled mode " + e.value + ` (valid: "auto", "true", "false")`
}
