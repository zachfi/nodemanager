package freebsd

import "flag"

type ControllerConfig struct {
	Poudriere    PoudriereConfig
	BastilleJail BastilleConfig
}

func (c *ControllerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Poudriere.RegisterFlagsAndApplyDefaults("poudriere", f)
	c.BastilleJail.RegisterFlagsAndApplyDefaults("bastille", f)
}

type PoudriereConfig struct{}

func (c *PoudriereConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {}

type BastilleConfig struct{}

func (c *BastilleConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {}
