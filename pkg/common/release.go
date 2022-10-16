package common

import (
	"context"
	"log"
	"runtime"
	"strings"

	"github.com/go-ini/ini"
)

type Sysinfo struct {
	Name    string
	Kernel  string
	Release string
	Version string
	Machine string
	Domain  string
	OS      struct {
		Release string // uname -r
		Name    string // os-release.NAME
		ID      string // os-release.ID

	}
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

func readOSRelease(configfile string) map[string]string {
	cfg, err := ini.Load(configfile)
	if err != nil {
		log.Fatal("Fail to read file: ", err)
	}

	ConfigParams := make(map[string]string)
	ConfigParams["ID"] = cfg.Section("").Key("ID").String()

	return ConfigParams
}

func getReleaseInfo() (r ReleaseInfo) {
	releaseInfo := readOSRelease("/etc/os-release")

	if val, ok := releaseInfo["ID"]; ok {
		r.ID = strings.ToLower(val)
	}

	if val, ok := releaseInfo["NAME"]; ok {
		r.Name = val
	}

	return
}

func GetSystemInfo(ctx context.Context) (sys *Sysinfo) {
	//
	sys.Runtime.Arch = runtime.GOARCH
	sys.Runtime.OS = runtime.GOOS

	osrelease := getReleaseInfo()
	sys.OS.ID = osrelease.ID
	sys.OS.Name = osrelease.Name

	args := []string{"-snrm"}
	output, _, err := runCommand("uname", args...)
	if err != nil {
		return
	}

	fields := strings.Fields(output)
	if len(fields) != 4 {
		return
	}

	sys.Kernel = fields[0]
	sys.Name = fields[1]
	sys.OS.Release = fields[2]
	sys.Machine = fields[3]

	return
}
