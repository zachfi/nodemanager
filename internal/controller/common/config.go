package common

import (
	"flag"
	"time"

	"github.com/grafana/dskit/backoff"
)

type ControllerConfig struct {
	ManagedNode ManagedNodeConfig
	ConfigSet   ConfigSetConfig
	Locker      LockerConfig
}

func (c *ControllerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.ManagedNode.RegisterFlagsAndApplyDefaults(prefix+".managednode", f)
	c.ConfigSet.RegisterFlagsAndApplyDefaults(prefix+".configset", f)
	c.Locker.RegisterFlagsAndApplyDefaults(prefix+".locker", f)
}

type ManagedNodeConfig struct {
	ForgivenessPeriod time.Duration
}

func (c *ManagedNodeConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&c.ForgivenessPeriod, prefix+".forgiveness-period", 1*time.Minute, "The duration to wait after a scheduled upgrade time before considering the upgrade missed and allowing a new upgrade to be scheduled.")
}

type ConfigSetConfig struct{}

func (c *ConfigSetConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
}

type LockerConfig struct {
	Backoff backoff.Config
}

func (c *LockerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&c.Backoff.MinBackoff, prefix+".backoff-min-period", 3*time.Second, "Minimum delay when backing off.")
	f.DurationVar(&c.Backoff.MaxBackoff, prefix+".backoff-max-period", 3*time.Minute, "Maximum delay when backing off.")

	c.Backoff.MaxRetries = 0
}
