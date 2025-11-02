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

func (c *JailConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {}
