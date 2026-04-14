package freebsd

import "flag"

type ControllerConfig struct {
	Poudriere PoudriereConfig
	Jail      JailConfig

	// AllowInJail disables the automatic skip of FreeBSD host-only controllers
	// (jail, poudriere) when nodemanager detects it is running inside a jail.
	// Set this only when the jail has been granted the necessary privileges:
	// ZFS dataset delegation, allow.mount.zfs, children.max, etc.
	AllowInJail bool
}

func (c *ControllerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Poudriere.RegisterFlagsAndApplyDefaults("poudriere", f)
	c.Jail.RegisterFlagsAndApplyDefaults("jail", f)
	f.BoolVar(&c.AllowInJail, prefix+".allow-in-jail", false, "Allow jail and poudriere controllers to start when running inside a FreeBSD jail (requires ZFS delegation and nested jail privileges on the host).")
}

type PoudriereConfig struct{}

func (c *PoudriereConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {}

type JailConfig struct {
	JailDataPath string
	ZfsDataset   string
	Mirror       string
}

func (c *JailConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&c.JailDataPath, prefix+".data-path", "/usr/local/nodemanager", "The base path where jail data is stored.")
	f.StringVar(&c.ZfsDataset, prefix+".zfs-dataset", "zroot/nodemanager", "The ZFS dataset to use for jail storage.")
	f.StringVar(&c.Mirror, prefix+".mirror", "https://download.freebsd.org/releases", "The FreeBSD mirror base URL for downloading release archives.")
}
