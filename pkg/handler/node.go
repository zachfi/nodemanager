package handler

import (
	"context"
)

type NodeHandler interface {
	Reboot(context.Context)
	Upgrade(context.Context) error
	Hostname() (string, error)
	InfoResolver
}

type InfoResolver interface {
	Info(context.Context) *SysInfo
}

type SysInfo struct {
	Name      string
	Kernel    string
	Version   string
	Machine   string
	Domain    string
	OS        OS
	Processor string
	Runtime   struct {
		Arch string
		OS   string
	}
}

type OS struct {
	Release string // uname -r
	Name    string // os-release.NAME
	ID      string // os-release.ID
}

type ReleaseInfo struct {
	ID   string
	Name string
}
