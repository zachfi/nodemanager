package jail

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/zachfi/nodemanager/pkg/handler"
)

const rcServiceDir = "/usr/local/etc/rc.d"

// rcServiceTmpl renders a self-contained rc.d(8) service script for a single
// jail. The script is placed in /usr/local/etc/rc.d/ (automatically sourced
// by FreeBSD's rc) so the jail starts at boot even when nodemanager is not
// running.
var rcServiceTmpl = template.Must(template.New("rc.d").Parse(`#!/bin/sh
#
# PROVIDE: jail_{{ .Name }}
# REQUIRE: NETWORKING
# BEFORE: DAEMON
# KEYWORD: shutdown nojail

. /etc/rc.subr

name="jail_{{ .Name }}"
rcvar="${name}_enable"
start_cmd="${name}_start"
stop_cmd="${name}_stop"

jail_{{ .Name }}_start()
{
	/usr/sbin/jail -c -f "{{ .ConfPath }}" "{{ .Name }}"
}

jail_{{ .Name }}_stop()
{
	/usr/sbin/jail -r "{{ .Name }}"
}

load_rc_config $name
: ${jail_{{ .Name }}_enable:="NO"}

run_rc_command "$1"
`))

type rcServiceData struct {
	Name     string
	ConfPath string
}

// ensureJailRCService writes /usr/local/etc/rc.d/jail_<name> and enables it
// via sysrc(8). Writes are skipped when the file content and rc.conf value
// are already correct, minimising disk activity on write-limited media.
func ensureJailRCService(ctx context.Context, exec handler.ExecHandler, name, confDir string) error {
	data := rcServiceData{
		Name:     name,
		ConfPath: filepath.Join(confDir, name+".conf"),
	}

	var buf bytes.Buffer
	if err := rcServiceTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering rc.d script for jail %s: %w", name, err)
	}

	if err := os.MkdirAll(rcServiceDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", rcServiceDir, err)
	}

	scriptPath := filepath.Join(rcServiceDir, "jail_"+name)
	existing, readErr := os.ReadFile(scriptPath)
	if readErr != nil || !bytes.Equal(existing, buf.Bytes()) {
		if err := os.WriteFile(scriptPath, buf.Bytes(), 0o755); err != nil {
			return fmt.Errorf("writing rc.d script for jail %s: %w", name, err)
		}
	}

	// Only call sysrc when the value isn't already YES to avoid a needless
	// write to /etc/rc.conf on every reconcile.
	rcvar := "jail_" + name + "_enable"
	out, _, _ := exec.RunCommand(ctx, "sysrc", "-n", rcvar)
	if strings.TrimSpace(out) != "YES" {
		if err := exec.SimpleRunCommand(ctx, "sysrc", rcvar+"=YES"); err != nil {
			return fmt.Errorf("enabling %s via sysrc: %w", rcvar, err)
		}
	}

	return nil
}

// removeJailRCService disables the per-jail rc.d service and removes its
// script. A missing script or rc.conf entry is treated as already clean.
func removeJailRCService(ctx context.Context, exec handler.ExecHandler, name string) error {
	rcvar := "jail_" + name + "_enable"
	out, _, _ := exec.RunCommand(ctx, "sysrc", "-n", rcvar)
	if strings.TrimSpace(out) == "YES" {
		// Best-effort; if the key is absent sysrc returns non-zero, which is fine.
		_ = exec.SimpleRunCommand(ctx, "sysrc", rcvar+"=NO")
	}

	scriptPath := filepath.Join(rcServiceDir, "jail_"+name)
	if err := os.Remove(scriptPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing rc.d script for jail %s: %w", name, err)
	}

	return nil
}
