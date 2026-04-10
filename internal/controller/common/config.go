package common

import (
	"flag"
	"time"

	"github.com/zachfi/nodemanager/internal/controller/freebsd"
	"github.com/zachfi/nodemanager/internal/notification"
	"github.com/zachfi/nodemanager/pkg/locker"
)

type FileBucketConfig struct {
	// Enabled turns on pre-write backup of managed files to a content-addressed store.
	Enabled bool `json:"enabled,omitempty"`
	// Path is the root directory of the filebucket store.
	// Defaults to /var/lib/nodemanager/filebucket.
	Path string `json:"path,omitempty"`
	// MaxFileSizeBytes skips backup when the existing on-disk file exceeds this
	// size. 0 means unlimited.
	MaxFileSizeBytes int64 `json:"maxFileSizeBytes,omitempty"`
	// MaxAge is how long blobs are retained in the bucket before GC removes them.
	// 0 means keep forever.
	MaxAge time.Duration `json:"maxAge,omitempty"`
}

func (c *FileBucketConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.BoolVar(&c.Enabled, prefix+".enabled", false, "Enable pre-write file backup to a content-addressed filebucket store")
	f.StringVar(&c.Path, prefix+".path", "/var/lib/nodemanager/filebucket", "Root directory of the filebucket content-addressed backup store")
	f.Int64Var(&c.MaxFileSizeBytes, prefix+".max-file-size-bytes", 102400, "Skip backup when existing file exceeds this size in bytes (0 = unlimited)")
	f.DurationVar(&c.MaxAge, prefix+".max-age", 7*24*time.Hour, "Remove blobs from the filebucket older than this duration (0 = keep forever)")
}

type ControllerConfig struct {
	MetricsAddr          string
	EnableLeaderElection bool
	ProbeAddr            string
	SecureMetrics        bool
	EnableHTTP2          bool
	Namespace            string

	ManagedNode  ManagedNodeConfig
	ConfigSet    ConfigSetConfig
	Locker       locker.Config
	FreeBSD      freebsd.ControllerConfig
	Notification notification.Config
}

func (c *ControllerConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&c.MetricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	f.BoolVar(&c.EnableLeaderElection, "leader-elect", false, "Enable leader election for controller manager. "+"Enabling this will ensure there is only one active controller manager.")
	f.StringVar(&c.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	f.BoolVar(&c.SecureMetrics, "metrics-secure", false, "If set the metrics endpoint is served securely")
	f.BoolVar(&c.EnableHTTP2, "enable-http2", false, "If set, HTTP/2 will be enabled for the metrics and webhook servers")

	f.StringVar(&c.Namespace, "namespace", "nodemanager", "The namespace to operate within")

	c.ManagedNode.RegisterFlagsAndApplyDefaults("managednode", f)
	c.ConfigSet.RegisterFlagsAndApplyDefaults("configset", f)
	c.Locker.RegisterFlagsAndApplyDefaults("locker", f)

	// OS specific controller settings
	c.FreeBSD.RegisterFlagsAndApplyDefaults("freebsd", f)

	c.Notification.RegisterFlagsAndApplyDefaults("notification", f)
}

type ManagedNodeConfig struct {
	ForgivenessPeriod time.Duration
	DrainTimeout      time.Duration
}

func (c *ManagedNodeConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&c.ForgivenessPeriod, prefix+".forgiveness-period", 1*time.Minute, "The duration to wait after a scheduled upgrade time before considering the upgrade missed and allowing a new upgrade to be scheduled.")
	f.DurationVar(&c.DrainTimeout, prefix+".drain-timeout", 5*time.Minute, "The maximum duration to wait for pods to drain from a kubernetes node before proceeding with the upgrade.")
}

type ConfigSetConfig struct {
	// Namespace is set by the controller harness from ControllerConfig.Namespace
	// at startup so the ConfigSet reconciler knows which namespace to query.
	// It is not exposed as a CLI flag (the top-level namespace flag is used instead).
	Namespace  string           `json:"-"`
	FileBucket FileBucketConfig `json:"fileBucket,omitempty"`
}

func (c *ConfigSetConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.FileBucket.RegisterFlagsAndApplyDefaults(prefix+".file-bucket", f)
}
