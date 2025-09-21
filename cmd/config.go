package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/drone/envsubst"
	"github.com/grafana/dskit/flagext"
	"github.com/zachfi/nodemanager/internal/controller/common"
	"github.com/zachfi/zkit/pkg/tracing"
	"sigs.k8s.io/yaml"
)

func NewDefaultConfig() *Config {
	defaultConfig := &Config{}
	defaultFS := flag.NewFlagSet("", flag.PanicOnError)
	defaultConfig.RegisterFlagsAndApplyDefaults("", defaultFS)
	return defaultConfig
}

type Config struct {
	Tracing          tracing.Config
	ControllerConfig common.ControllerConfig
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Tracing.RegisterFlagsAndApplyDefaults("tracing", f)
	c.ControllerConfig.RegisterFlagsAndApplyDefaults("controller", f)
}

func loadConfig() (*Config, bool, error) {
	const (
		configFileOption      = "config.file"
		configExpandEnvOption = "config.expand-env"
		configVerifyOption    = "config.verify"
	)

	var (
		configFile      string
		configExpandEnv bool
		configVerify    bool
	)

	args := os.Args[1:]
	config := &Config{}

	// first get the config file
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&configFile, configFileOption, "", "")
	fs.BoolVar(&configExpandEnv, configExpandEnvOption, false, "")
	fs.BoolVar(&configVerify, configVerifyOption, false, "")

	// Try to find -config.file & -config.expand-env flags. As Parsing stops on the first error, eg. unknown flag,
	// we simply try remaining parameters until we find config flag, or there are no params left.
	// (ContinueOnError just means that flag.Parse doesn't call panic or os.Exit, but it returns error, which we ignore)
	for len(args) > 0 {
		_ = fs.Parse(args)
		args = args[1:]
	}

	// load config defaults and register flags
	config.RegisterFlagsAndApplyDefaults("", flag.CommandLine)

	// overlay with config file if provided
	if configFile != "" {
		buff, err := os.ReadFile(configFile)
		if err != nil {
			return nil, false, fmt.Errorf("failed to read configFile %s: %w", configFile, err)
		}

		if configExpandEnv {
			s, err := envsubst.EvalEnv(string(buff))
			if err != nil {
				return nil, false, fmt.Errorf("failed to expand env vars from configFile %s: %w", configFile, err)
			}
			buff = []byte(s)
		}

		err = yaml.UnmarshalStrict(buff, config)
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse configFile %s: %w", configFile, err)
		}

	}

	// overlay with cli
	flagext.IgnoredFlag(flag.CommandLine, configFileOption, "Configuration file to load")
	flagext.IgnoredFlag(flag.CommandLine, configExpandEnvOption, "Whether to expand environment variables in config file")
	flagext.IgnoredFlag(flag.CommandLine, configVerifyOption, "Verify configuration and exit")
	flag.Parse()

	return config, configVerify, nil
}

func configIsValid(config *Config) bool {
	// Warn the user for suspect configurations
	// if warnings := config.CheckConfig(); len(warnings) != 0 {
	// 	level.Warn(log.Logger).Log("msg", "-- CONFIGURATION WARNINGS --")
	// 	for _, w := range warnings {
	// 		output := []any{"msg", w.Message}
	// 		if w.Explain != "" {
	// 			output = append(output, "explain", w.Explain)
	// 		}
	// 		level.Warn(log.Logger).Log(output...)
	// 	}
	// 	return false
	// }
	return true
}
