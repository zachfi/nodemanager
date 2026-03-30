package jail

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zachfi/nodemanager/pkg/handler"
)

const jlsCmd = "/usr/sbin/jls"

// jlsOutput is the top-level structure returned by jls -v --libxo=json.
type jlsOutput struct {
	Version         string          `json:"__version"`
	JailInformation jailInformation `json:"jail-information"`
}

type jailInformation struct {
	Jails []Jail `json:"jail"`
}

// listRunningJails queries the kernel for all currently running jails by
// parsing the JSON output of jls(8). It returns an empty slice (not an error)
// when no jails are running.
func listRunningJails(ctx context.Context, exec handler.ExecHandler) ([]Jail, error) {
	out, _, err := exec.RunCommand(ctx, jlsCmd, "-v", "--libxo=json")
	if err != nil {
		return nil, fmt.Errorf("running jls: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	var result jlsOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("parsing jls output: %w", err)
	}
	return result.JailInformation.Jails, nil
}

// isJailRunning reports whether the named jail is currently active.
func isJailRunning(ctx context.Context, exec handler.ExecHandler, name string) (bool, error) {
	jails, err := listRunningJails(ctx, exec)
	if err != nil {
		return false, err
	}
	for _, j := range jails {
		if j.Name == name {
			return true, nil
		}
	}
	return false, nil
}
