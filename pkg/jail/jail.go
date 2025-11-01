package jail

import "github.com/zachfi/nodemanager/pkg/handler"

const jailCmd = "/usr/sbin/jail"

type Jailer interface {
	Start() error
	Stop() error
	// jls -v --libxo=json
	// {"__version": "2", "jail-information": {"jail": [{"jid":2,"hostname":"git7","path":"/usr/local/bastille/jails/git7/root","name":"git7","state":"ACTIVE","cpusetid":5, "ipv4_addrs": ["172.16.20.98"], "ipv6_addrs": []}, {"jid":105,"hostname":"sto2","path":"/usr/local/bastille/jails/sto2/root","name":"sto2","state":"ACTIVE","cpusetid":7, "ipv4_addrs": ["172.16.20.112"], "ipv6_addrs": ["2001:470:e8af:20::532"]}, {"jid":116,"hostname":"git6","path":"/usr/local/bastille/jails/git6/root","name":"git6","state":"ACTIVE","cpusetid":3, "ipv4_addrs": ["172.16.20.99"], "ipv6_addrs": ["2001:470:e8af:20::577"]}]}}
	List() ([]Jail, error)

	// Notes
	// service jail start <jail name>
	// service jail stop <jail name>
	// chflags -R 0 /usr/local/jails/containers/classic
	// rm -rf /usr/local/jails/containers/classic
	// pkg -j classic install nginx-lite
	// jexec -u root jailname
	// jexec -l jailname service nginx stop
	// Update
	// freebsd-update -j classic fetch install
	// service jail restart classic
	// Upgrade
	// freebsd-update -j classic -r 13.2-RELEASE upgrade
	// freebsd-update -j classic install
	// service jail restart classic
	// freebsd-update -j classic install
	// service jail restart classic

	// manually start/stop
	// jail -f /usr/local/jails/borat/jail.conf -c borat
	// jail -f /usr/local/jails/borat/jail.conf -r borat
}

// Jail represents a FreeBSD jail instance.
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

type jailer struct {
	exec handler.ExecHandler
}

func NewJailer(exec handler.ExecHandler) Jailer {
	return &jailer{
		exec: exec,
	}
}

func (j *jailer) Start() error {
	// Implementation to start a jail
	return nil
}

func (j *jailer) Stop() error {
	// Implementation to stop a jail
	return nil
}

func (j *jailer) List() ([]Jail, error) {
	// Implementation to list jails
	return []Jail{}, nil
}
