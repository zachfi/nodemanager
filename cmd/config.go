package main

import (
	"flag"

	"github.com/zachfi/nodemanager/internal/controller/common"
	"github.com/zachfi/zkit/pkg/tracing"
)

func NewDefaultConfig() *Config {
	defaultConfig := &Config{}
	defaultFS := flag.NewFlagSet("", flag.PanicOnError)
	defaultConfig.RegisterFlagsAndApplyDefaults("", defaultFS)
	return defaultConfig
}

type Config struct {
	MetricsAddr          string
	EnableLeaderElection bool
	ProbeAddr            string
	SecureMetrics        bool
	EnableHTTP2          bool

	Namespace string

	Tracing          tracing.Config
	ControllerConfig common.ControllerConfig
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	// Controller options
	f.StringVar(&c.MetricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	f.BoolVar(&c.EnableLeaderElection, "leader-elect", false, "Enable leader election for controller manager. "+"Enabling this will ensure there is only one active controller manager.")
	f.StringVar(&c.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&c.SecureMetrics, "metrics-secure", false, "If set the metrics endpoint is served securely")
	flag.BoolVar(&c.EnableHTTP2, "enable-http2", false, "If set, HTTP/2 will be enabled for the metrics and webhook servers")

	f.StringVar(&c.Namespace, "namespace", "nodemanager", "The namespace to operate within")

	c.Tracing.RegisterFlagsAndApplyDefaults(prefix, f)
	c.ControllerConfig.RegisterFlagsAndApplyDefaults(prefix, f)
}
