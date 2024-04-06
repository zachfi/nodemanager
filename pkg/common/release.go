package common

import (
	"log"
	"runtime"
	"strings"

	"github.com/go-ini/ini"
)

type SysInfoResolver interface {
	Info() *SysInfo
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

type ReleaseInfo struct {
	ID   string
	Name string
}

type OS struct {
	Release string // uname -r
	Name    string // os-release.NAME
	ID      string // os-release.ID
}

type UnameInfoResolver struct{}

func (r *UnameInfoResolver) Info() *SysInfo {
	sys := &SysInfo{}
	//
	sys.Runtime.Arch = runtime.GOARCH
	sys.Runtime.OS = runtime.GOOS

	osrelease := r.getReleaseInfo()
	sys.OS.ID = osrelease.ID
	sys.OS.Name = osrelease.Name

	args := []string{"-snrm"}
	output, _, err := runCommand("uname", args...)
	if err != nil {
		return sys
	}

	fields := strings.Fields(output)
	if len(fields) != 4 {
		return sys
	}

	sys.Kernel = fields[0]
	sys.Name = fields[1]
	sys.OS.Release = fields[2]
	sys.Machine = fields[3]

	return sys
}

func (r *UnameInfoResolver) getReleaseInfo() (info ReleaseInfo) {
	releaseInfo := r.readOSRelease("/etc/os-release")

	if val, ok := releaseInfo["ID"]; ok {
		info.ID = strings.ToLower(val)
	}

	if val, ok := releaseInfo["NAME"]; ok {
		info.Name = val
	}

	return
}

func (r *UnameInfoResolver) readOSRelease(configfile string) map[string]string {
	cfg, err := ini.Load(configfile)
	if err != nil {
		log.Fatal("Fail to read file: ", err)
	}

	ConfigParams := make(map[string]string)
	ConfigParams["ID"] = cfg.Section("").Key("ID").String()

	return ConfigParams
}

type MockInfoResolver struct {
	info *SysInfo
}

func (r *MockInfoResolver) Info() *SysInfo {
	return r.info
}
