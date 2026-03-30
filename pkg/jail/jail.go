package jail

// Jail represents the runtime state of a FreeBSD jail as reported by jls(8).
type Jail struct {
	ID        int      `json:"jid"`
	Hostname  string   `json:"hostname"`
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	State     string   `json:"state"`
	CpusetID  int      `json:"cpusetid"`
	IPv4Addrs []string `json:"ipv4_addrs"`
	IPv6Addrs []string `json:"ipv6_addrs"`
}
