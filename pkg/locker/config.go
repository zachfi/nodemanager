package locker

import (
	"flag"
	"time"

	"github.com/grafana/dskit/backoff"
)

type Config struct {
	Backoff       backoff.Config
	LeaseDuration time.Duration
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&c.Backoff.MinBackoff, prefix+".backoff-min-period", 3*time.Second, "Minimum delay when backing off.")
	f.DurationVar(&c.Backoff.MaxBackoff, prefix+".backoff-max-period", 3*time.Minute, "Maximum delay when backing off.")
	f.IntVar(&c.Backoff.MaxRetries, prefix+".backoff-max-retries", 0, "The maximum number of retries. 0 means no limit.")
	f.DurationVar(&c.LeaseDuration, prefix+".lease-duration", 7*time.Minute, "The time to hold a lease")
}
