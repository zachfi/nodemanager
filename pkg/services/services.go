package services

type ServiceStatus int64

const (
	UnknownServiceStatus ServiceStatus = iota
	Running
	Stopped
)

var StatusByName map[string]ServiceStatus = map[string]ServiceStatus{
	"unknown": UnknownServiceStatus,
	"running": Running,
	"stopped": Stopped,
}

func (s ServiceStatus) String() string {
	switch s {
	case UnknownServiceStatus:
		return "unknown"
	case Running:
		return "running"
	case Stopped:
		return "stopped"
	}
	return "unknown"
}

func ServiceStatusFromString(status string) ServiceStatus {
	if s, ok := StatusByName[status]; ok {
		return s
	}
	return UnknownServiceStatus
}
