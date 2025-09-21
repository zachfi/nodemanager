package common

import (
	"flag"
	"time"

	"github.com/grafana/dskit/backoff"
)

type ControllerConfig struct {
	MetricsAddr          string
	EnableLeaderElection bool
	ProbeAddr            string
	SecureMetrics        bool
	EnableHTTP2          bool
	Namespace            string

	ManagedNode ManagedNodeConfig
	ConfigSet   ConfigSetConfig
	Locker      LockerConfig
}

func (c *ControllerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&c.MetricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	f.BoolVar(&c.EnableLeaderElection, "leader-elect", false, "Enable leader election for controller manager. "+"Enabling this will ensure there is only one active controller manager.")
	f.StringVar(&c.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&c.SecureMetrics, "metrics-secure", false, "If set the metrics endpoint is served securely")
	flag.BoolVar(&c.EnableHTTP2, "enable-http2", false, "If set, HTTP/2 will be enabled for the metrics and webhook servers")

	f.StringVar(&c.Namespace, "namespace", "nodemanager", "The namespace to operate within")

	c.ManagedNode.RegisterFlagsAndApplyDefaults("managednode", f)
	c.ConfigSet.RegisterFlagsAndApplyDefaults("configset", f)
	c.Locker.RegisterFlagsAndApplyDefaults("locker", f)
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
	f.IntVar(&c.Backoff.MaxRetries, prefix+".backoff-max-retries", 0, "The maximum number of retries. 0 means no limit.")
}
