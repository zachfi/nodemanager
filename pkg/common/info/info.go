package info

import (
	"context"
	"log"
	"runtime"
	"strings"

	"github.com/go-ini/ini"
	"github.com/zachfi/nodemanager/pkg/execs"
	"github.com/zachfi/nodemanager/pkg/handler"
)

var _ handler.InfoResolver = (*infoResolver)(nil)

func NewInfoResolver() handler.InfoResolver {
	return &infoResolver{&execs.ExecHandlerCommon{}}
}

type infoResolver struct {
	exec handler.ExecHandler
}

func (r *infoResolver) Info(ctx context.Context) *handler.SysInfo {
	sys := &handler.SysInfo{}

	sys.Runtime.Arch = runtime.GOARCH
	sys.Runtime.OS = runtime.GOOS

	osrelease := r.getReleaseInfo()
	sys.OS.ID = osrelease.ID
	sys.OS.Name = osrelease.Name

	args := []string{"-snrm"}
	output, _, err := r.exec.RunCommand(ctx, "uname", args...)
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

func (r *infoResolver) getReleaseInfo() (info handler.ReleaseInfo) {
	releaseInfo := r.readOSRelease("/etc/os-release")

	if val, ok := releaseInfo["ID"]; ok {
		info.ID = strings.ToLower(val)
	}

	if val, ok := releaseInfo["NAME"]; ok {
		info.Name = val
	}

	return
}

func (r *infoResolver) readOSRelease(configfile string) map[string]string {
	cfg, err := ini.Load(configfile)
	if err != nil {
		log.Fatal("Fail to read file: ", err)
	}

	ConfigParams := make(map[string]string)
	ConfigParams["ID"] = cfg.Section("").Key("ID").String()

	return ConfigParams
}
