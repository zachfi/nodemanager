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
{{ end -}}
{{- range .PoststopCmds }}
	exec.poststop += "{{ . }}";
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
	Parameters   map[string]string
	PrestartCmds []string
	PoststopCmds []string
}

// writeJailConf renders and writes <confDir>/<name>.conf.
// It returns true when the file already existed on disk with different content,
// indicating that a running jail must be restarted to pick up the change.
// A new file (first write) returns false — the jail hasn't started yet.
func writeJailConf(confDir, name, jailRoot string, spec freebsdv1.JailSpec) (bool, error) {
	hostname := spec.Hostname
	if hostname == "" {
		hostname = name
	}

	// Build exec.prestart commands:
	//   1. Loop-unmount stale devfs layers from previous failed starts.
	//      Nullfs mounts are handled by StartJail Go code before jail -c to
	//      avoid the ZFS vnode lock cycle that occurs when jail(8) path-resolves
	//      the jail root before running exec.prestart hooks.
	//   2. Belt-and-suspenders IP alias cleanup in case a daemon re-added an
	//      alias in the narrow window between StartJail's removeIPAliasesForSpec
	//      call and jail(8) adding the aliases itself.
	prestartCmds := prestartUnmounts(jailRoot, nil) // devfs only
	prestartCmds = append(prestartCmds, prestartIPCleanup(spec)...)

	// exec.poststop unmounts the mounts after the jail stops.
	poststopCmds := poststopUnmounts(jailRoot, spec.Mounts)

	// Build a filtered copy of Parameters: strip mount.fstab because mounts are
	// now managed via exec.prestart/exec.poststop. Keeping mount.fstab in
	// Parameters would cause jail(8) to process the fstab file while holding the
	// jail-root VFS lock, deadlocking nullfs mounts on FreeBSD.
	params := make(map[string]string, len(spec.Parameters))
	for k, v := range spec.Parameters {
		if k == "mount.fstab" {
			continue
		}
		params[k] = v
	}

	data := jailConfData{
		Name:         name,
		Hostname:     hostname,
		Path:         jailRoot,
		Interface:    spec.Interface,
		Inets:        spec.Inets,
		Inet6s:       spec.Inet6s,
		Release:      spec.Release,
		Parameters:   params,
		PrestartCmds: prestartCmds,
		PoststopCmds: poststopCmds,
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

// prestartMounts returns exec.prestart shell commands that mount each declared
// filesystem before the jail is created. Mounting in exec.prestart (rather than
// via mount.fstab) avoids the FreeBSD VFS deadlock that occurs when jail(8)
// holds the jail-root lock while processing fstab entries.
// Mounts are applied shallowest-first so parent directories exist before
// nested mounts are attempted.
func prestartMounts(jailRoot string, mounts []freebsdv1.JailMount) []string {
	if len(mounts) == 0 {
		return nil
	}

	// Sort shallowest (shortest) paths first for mounting.
	sorted := make([]freebsdv1.JailMount, len(mounts))
	copy(sorted, mounts)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].JailPath) < len(sorted[j].JailPath)
	})

	cmds := make([]string, len(sorted))
	for i, m := range sorted {
		fsType := m.Type
		if fsType == "" {
			fsType = "nullfs"
		}
		opts := "rw"
		if m.ReadOnly {
			opts = "ro"
		}
		dest := filepath.Join(jailRoot, m.JailPath)
		cmds[i] = fmt.Sprintf("mount -t %s -o %s %s %s", fsType, opts, m.HostPath, dest)
	}
	return cmds
}

// poststopUnmounts returns exec.poststop shell commands that unmount each
// declared filesystem after the jail stops. Deepest paths are unmounted first.
func poststopUnmounts(jailRoot string, mounts []freebsdv1.JailMount) []string {
	if len(mounts) == 0 {
		return nil
	}

	paths := make([]string, len(mounts))
	for i, m := range mounts {
		paths[i] = filepath.Join(jailRoot, m.JailPath)
	}
	sort.Slice(paths, func(i, j int) bool {
		return len(paths[i]) > len(paths[j])
	})

	cmds := make([]string, len(paths))
	for i, p := range paths {
		cmds[i] = fmt.Sprintf("umount -f %s 2>/dev/null || true", p)
	}
	return cmds
}

// prestartIPCleanup returns exec.prestart shell commands that remove IP aliases
// for each address declared in the jail spec. This runs immediately before
// jail(8) adds the aliases itself, closing the window in which daemons such as
// OpenBGPD can re-add an alias between StopJail and the next StartJail and
// cause jail(8) to fail with "File exists".
func prestartIPCleanup(spec freebsdv1.JailSpec) []string {
	if spec.Interface == "" || (len(spec.Inets) == 0 && len(spec.Inet6s) == 0) {
		return nil
	}
	var cmds []string
	for _, addr := range spec.Inets {
		cmds = append(cmds, fmt.Sprintf("ifconfig %s inet %s -alias 2>/dev/null || true", spec.Interface, stripCIDR(addr)))
	}
	for _, addr := range spec.Inet6s {
		cmds = append(cmds, fmt.Sprintf("ifconfig %s inet6 %s -alias 2>/dev/null || true", spec.Interface, stripCIDR(addr)))
	}
	return cmds
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
