package jail

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

var fstabTmpl = template.Must(template.New("fstab").Parse(
	`# Managed by nodemanager — do not edit manually.
{{ range .Entries -}}
{{ .Device }}	{{ .Mountpoint }}	{{ .Type }}	{{ .Options }}	0	0
{{ end -}}
`))

type fstabData struct {
	Entries []fstabEntry
}

type fstabEntry struct {
	Device     string
	Mountpoint string
	Type       string
	Options    string
}

// writeFstab renders the per-jail fstab to fstabPath.
// jailRoot is the absolute path to the jail's root filesystem so that
// JailPath values are resolved to their host-side mountpoints.
func writeFstab(fstabPath, jailRoot string, mounts []freebsdv1.JailMount) error {
	entries := make([]fstabEntry, 0, len(mounts))
	for _, m := range mounts {
		fsType := m.Type
		if fsType == "" {
			fsType = "nullfs"
		}
		opts := "rw"
		if m.ReadOnly {
			opts = "ro"
		}
		entries = append(entries, fstabEntry{
			Device:     m.HostPath,
			Mountpoint: filepath.Join(jailRoot, m.JailPath),
			Type:       fsType,
			Options:    opts,
		})
	}

	var buf bytes.Buffer
	if err := fstabTmpl.Execute(&buf, fstabData{Entries: entries}); err != nil {
		return fmt.Errorf("rendering fstab: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(fstabPath), 0o755); err != nil {
		return fmt.Errorf("creating fstab directory: %w", err)
	}
	if err := os.WriteFile(fstabPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing fstab %s: %w", fstabPath, err)
	}
	return nil
}

// removeFstab deletes the per-jail fstab file if it exists.
func removeFstab(fstabPath string) error {
	if err := os.Remove(fstabPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing fstab %s: %w", fstabPath, err)
	}
	return nil
}
