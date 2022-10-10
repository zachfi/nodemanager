package common

import (
	"context"
	"log"
	"runtime"
	"syscall"

	"github.com/go-ini/ini"
)

type Sysinfo struct {
	Name      string
	Node      string
	Release   string
	Version   string
	Machine   string
	Domain    string
	OS        string
	OSRelease string
	Processor string
}

type ReleaseInfo struct {
	ID string
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

func getReleaseInfo(configfile string) ReleaseInfo {
	releaseInfo := readOSRelease("/etc/os-release")
	return ReleaseInfo{
		ID: releaseInfo["ID"],
	}
}

func GetSystemInfo(ctx context.Context) *Sysinfo {
	osrelease := getReleaseInfo("/etc/os-release")

	var utsname syscall.Utsname
	_ = syscall.Uname(&utsname)
	sys := Sysinfo{
		Name:      utsnameToString(utsname.Sysname),
		Node:      utsnameToString(utsname.Nodename),
		Release:   utsnameToString(utsname.Release),
		Version:   utsnameToString(utsname.Version),
		Machine:   utsnameToString(utsname.Machine),
		Domain:    utsnameToString(utsname.Domainname),
		OS:        runtime.GOOS,
		OSRelease: osrelease.ID,
		// processor: getProcessorName(),
	}
	return &sys
}

func utsnameToString(unameArray [65]int8) string {
	var byteString [65]byte
	var indexLength int
	for ; unameArray[indexLength] != 0; indexLength++ {
		byteString[indexLength] = uint8(unameArray[indexLength])
	}
	return string(byteString[:indexLength])
}
