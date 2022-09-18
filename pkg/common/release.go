package common

import (
	"log"

	"github.com/go-ini/ini"
)

func ReadOSRelease(configfile string) map[string]string {
	cfg, err := ini.Load(configfile)
	if err != nil {
		log.Fatal("Fail to read file: ", err)
	}

	ConfigParams := make(map[string]string)
	ConfigParams["ID"] = cfg.Section("").Key("ID").String()

	return ConfigParams
}

func OsReleaseID() string {
	releaseInfo := ReadOSRelease("/etc/os-release")
	return releaseInfo["ID"]
}
