package notification

import "flag"

// Config holds settings for the gRPC notification server.
type Config struct {
	Enabled    bool   `json:"enabled,omitempty"`
	SocketPath string `json:"socketPath,omitempty"`
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.BoolVar(&c.Enabled, prefix+".enabled", false, "Enable the gRPC notification server")
	f.StringVar(&c.SocketPath, prefix+".socket-path", "/run/nodemanager/notify.sock", "Unix domain socket path for the notification gRPC server")
}
