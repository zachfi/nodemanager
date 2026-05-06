package jail

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// DefaultJailConfDir is the standard location for per-jail configuration
// fragments on modern FreeBSD.
const DefaultJailConfDir = "/etc/jail.conf.d"

var jailConfTmpl = template.Must(template.New("jail.conf").Funcs(template.FuncMap{
	"join": strings.Join,
}).Parse(`{{ .Name }} {
	host.hostname = "{{ .Hostname }}";
	path          = "{{ .Path }}";

	exec.start = "/bin/sh /etc/rc";
	exec.stop  = "/bin/sh /etc/rc.shutdown jail";
	exec.clean;
	exec.consolelog = "/var/log/jail_{{ .Name }}_console.log";
{{- range .PrestartCmds }}
	exec.prestart += "{{ . }}";
{{ end }}
	mount.devfs;
	devfs_ruleset = 4;
	enforce_statfs = 2;
	securelevel = 2;

	allow.raw_sockets;
{{ if .Interface }}
	interface = "{{ .Interface }}";
{{ end -}}
{{ if .Inets }}
	ip4.addr = {{ join .Inets ", " }};
{{ end -}}
{{ if .Inet6s }}
	ip6.addr = {{ join .Inet6s ", " }};
	ip6 = new;
{{ end -}}
{{ if .Release }}
	osrelease = "{{ .Release }}";
{{ end -}}
{{ if .FstabPath }}
	mount.fstab = "{{ .FstabPath }}";
{{ end -}}
{{ range $k, $v := .Parameters }}
{{ if $v }}	{{ $k }} = {{ $v }};
{{ else }}	{{ $k }};
{{ end -}}
{{ end -}}
}
`))

type jailConfData struct {
	Name         string
	Hostname     string
	Path         string
	Interface    string
	Inets        []string
	Inet6s       []string
	Release      string
	FstabPath    string
	Parameters   map[string]string
	PrestartCmds []string
}

// writeJailConf renders and writes <confDir>/<name>.conf.
// It returns true when the file already existed on disk with different content,
// indicating that a running jail must be restarted to pick up the change.
// A new file (first write) returns false — the jail hasn't started yet.
func writeJailConf(confDir, name, jailRoot, fstabPath string, spec freebsdv1.JailSpec) (bool, error) {
	hostname := spec.Hostname
	if hostname == "" {
		hostname = name
	}

	// Build exec.prestart unmount commands to clean up any stale mounts left
	// by a previously failed start.  Paths are sorted deepest-first so nested
	// mounts are unmounted before their parents.
	prestartCmds := prestartUnmounts(jailRoot, spec.Mounts)

	data := jailConfData{
		Name:         name,
		Hostname:     hostname,
		Path:         jailRoot,
		Interface:    spec.Interface,
		Inets:        spec.Inets,
		Inet6s:       spec.Inet6s,
		Release:      spec.Release,
		FstabPath:    fstabPath,
		Parameters:   spec.Parameters,
		PrestartCmds: prestartCmds,
	}

	var buf bytes.Buffer
	if err := jailConfTmpl.Execute(&buf, data); err != nil {
		return false, fmt.Errorf("rendering jail.conf for %s: %w", name, err)
	}

	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", confDir, err)
	}

	confPath := filepath.Join(confDir, name+".conf")
	existing, readErr := os.ReadFile(confPath)

	if err := os.WriteFile(confPath, buf.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", confPath, err)
	}

	// Only signal a restart-needed change when the file pre-existed with
	// different content.  A brand-new file means the jail hasn't started yet.
	return readErr == nil && !bytes.Equal(existing, buf.Bytes()), nil
}

// removeJailConf deletes <confDir>/<name>.conf if it exists.
func removeJailConf(confDir, name string) error {
	path := filepath.Join(confDir, name+".conf")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing jail.conf for %s: %w", name, err)
	}
	return nil
}

// prestartUnmounts returns exec.prestart shell commands that force-unmount any
// stale mounts left by a previously failed jail start.  devfs is always
// included (it is mounted by the jail machinery before fstab mounts).
// Additional entries cover each configured nullfs/fstab mount.
// Paths are ordered deepest-first so nested mounts are cleared before parents.
func prestartUnmounts(jailRoot string, mounts []freebsdv1.JailMount) []string {
	// Collect all host-side mountpoints that need cleanup.
	paths := make([]string, 0, len(mounts)+1)
	paths = append(paths, filepath.Join(jailRoot, "dev"))
	for _, m := range mounts {
		paths = append(paths, filepath.Join(jailRoot, m.JailPath))
	}

	// Sort deepest (longest) paths first.
	sort.Slice(paths, func(i, j int) bool {
		return len(paths[i]) > len(paths[j])
	})

	cmds := make([]string, len(paths))
	for i, p := range paths {
		if strings.HasSuffix(p, "/dev") {
			// devfs can accumulate multiple stacked mounts from previous failed
			// starts. Loop until umount reports nothing left to unmount so all
			// stale devfs layers are cleared before the jail recreates its own.
			cmds[i] = fmt.Sprintf("while umount -f %s 2>/dev/null; do true; done", p)
		} else {
			cmds[i] = fmt.Sprintf("umount -f %s 2>/dev/null || true", p)
		}
	}
	return cmds
}
