package common

import "fmt"

type ServiceHandler interface {
	Enable(string) error
	Disable(string) error
	Start(string) error
	Stop(string) error
	Status(string) (string, error)
}

type ServiceStatus int64

const (
	Unknown ServiceStatus = iota
	Running
	Stopped
)

func (s ServiceStatus) String() string {
	switch s {
	case Unknown:
		return "unknown"
	case Running:
		return "running"
	case Stopped:
		return "stopped"
	}
	return "unknown"
}

func GetServiceHandler() (ServiceHandler, error) {
	switch OsReleaseID() {
	case "arch":
		return &ServiceHandler_Systemd{}, nil
	case "freebsd":
		return &ServiceHandler_FreeBSD{}, nil
	}

	return &ServiceHandler_Null{}, fmt.Errorf("service handler not available for system")
}

type ServiceHandler_Null struct{}

func (h *ServiceHandler_Null) Enable(_ string) error           { return nil }
func (h *ServiceHandler_Null) Disable(_ string) error          { return nil }
func (h *ServiceHandler_Null) Start(_ string) error            { return nil }
func (h *ServiceHandler_Null) Stop(_ string) error             { return nil }
func (h *ServiceHandler_Null) Status(_ string) (string, error) { return Unknown.String(), nil }

// FREEBSD
type ServiceHandler_FreeBSD struct{}

func (h *ServiceHandler_FreeBSD) Enable(name string) error {
	return simpleRunCommand("sysrc", name+"_enable=YES")
}

func (h *ServiceHandler_FreeBSD) Disable(name string) error {
	return simpleRunCommand("sysrc", name+"_enable=NO")
}

func (h *ServiceHandler_FreeBSD) Start(name string) error {
	return simpleRunCommand("service", name, "start")
}

func (h *ServiceHandler_FreeBSD) Stop(name string) error {
	return simpleRunCommand("service", name, "start")
}

func (h *ServiceHandler_FreeBSD) Status(name string) (string, error) {
	_, exit, err := runCommand("service", name, "status")
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}

// LINUX
type ServiceHandler_Systemd struct{}

func (h *ServiceHandler_Systemd) Enable(name string) error {
	return simpleRunCommand("sysrc", name+"_enable=YES")
}
func (h *ServiceHandler_Systemd) Disable(name string) error {
	return simpleRunCommand("sysrc", name+"_enable=NO")
}
func (h *ServiceHandler_Systemd) Start(name string) error {
	return simpleRunCommand("service", name, "start")
}
func (h *ServiceHandler_Systemd) Stop(name string) error {
	return simpleRunCommand("service", name, "start")
}

func (h *ServiceHandler_Systemd) Status(name string) (string, error) {
	_, exit, err := runCommand("systemctl", "is-active", "--quiet", name)
	if exit == 0 {
		return Running.String(), nil
	}

	return Stopped.String(), err
}
