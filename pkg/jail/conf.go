package jail

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// DefaultJailConfDir is the standard location for per-jail configuration
// fragments on modern FreeBSD.
const DefaultJailConfDir = "/etc/jail.conf.d"

var jailConfTmpl = template.Must(template.New("jail.conf").Parse(`{{ .Name }} {
	host.hostname = "{{ .Hostname }}";
	path          = "{{ .Path }}";

	exec.start = "/bin/sh /etc/rc";
	exec.stop  = "/bin/sh /etc/rc.shutdown jail";
	exec.clean;

	mount.devfs;
	devfs_ruleset = 4;

	allow.raw_sockets;
{{ if .Interface }}
	interface = "{{ .Interface }}";
{{ end -}}
{{ if .Inet }}
	ip4.addr = {{ .Inet }};
{{ end -}}
{{ if .Inet6 }}
	ip6.addr = {{ .Inet6 }};
{{ end -}}
{{ if .FstabPath }}
	mount.fstab = "{{ .FstabPath }}";
{{ end -}}
}
`))

type jailConfData struct {
	Name      string
	Hostname  string
	Path      string
	Interface string
	Inet      string
	Inet6     string
	// FstabPath is non-empty when the jail has extra mounts.
	FstabPath string
}

// writeJailConf renders and writes <confDir>/<name>.conf.
// It creates the directory if necessary and overwrites any existing file.
func writeJailConf(confDir, name, jailRoot, fstabPath string, spec freebsdv1.JailSpec) error {
	hostname := spec.Hostname
	if hostname == "" {
		hostname = name
	}

	data := jailConfData{
		Name:      name,
		Hostname:  hostname,
		Path:      jailRoot,
		Interface: spec.Interface,
		Inet:      spec.Inet,
		Inet6:     spec.Inet6,
		FstabPath: fstabPath,
	}

	var buf bytes.Buffer
	if err := jailConfTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering jail.conf for %s: %w", name, err)
	}

	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", confDir, err)
	}

	confPath := filepath.Join(confDir, name+".conf")
	if err := os.WriteFile(confPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", confPath, err)
	}
	return nil
}

// removeJailConf deletes <confDir>/<name>.conf if it exists.
func removeJailConf(confDir, name string) error {
	path := filepath.Join(confDir, name+".conf")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing jail.conf for %s: %w", name, err)
	}
	return nil
}
