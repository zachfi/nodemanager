package freebsd

import "flag"

type ControllerConfig struct {
	Poudriere PoudriereConfig
	Jail      JailConfig
}

func (c *ControllerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Poudriere.RegisterFlagsAndApplyDefaults("poudriere", f)
	c.Jail.RegisterFlagsAndApplyDefaults("jail", f)
}

type PoudriereConfig struct{}

func (c *PoudriereConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {}

type JailConfig struct {
	JailDataPath string
	ZfsDataset   string
}

func (c *JailConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&c.JailDataPath, prefix+".data-path", "/usr/local/nodemanager", "The base path where jail data is stored.")
	f.StringVar(&c.ZfsDataset, prefix+".zfs-dataset", "zroot/nodemanager", "The ZFS dataset to use for jail storage.")
}
