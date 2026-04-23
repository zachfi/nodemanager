package jail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/zfs"
)

const (
	// JailRootDir is the subdirectory under basePath that holds per-jail datasets.
	JailRootDir = "jails"
	// ReleaseRootDir is the subdirectory under basePath that holds release datasets.
	ReleaseRootDir = "releases"
)

// Manager provisions, removes, and controls the lifecycle of FreeBSD jails
// backed by ZFS datasets.
//
// Dataset layout (relative to the configured base dataset, e.g. zroot/nodemanager):
//
//	<dataset>/releases/<version>          — extracted FreeBSD base (one per release)
//	<dataset>/jails/<name>                — per-jail container dataset
//	<dataset>/jails/<name>/root           — ZFS clone of the release snapshot
//
// Filesystem layout (relative to basePath, e.g. /usr/local/nodemanager):
//
//	releases/<version>/                   — release root (bin/freebsd-version present when ready)
//	jails/<name>/                         — per-jail directory (fstab, logs)
//	jails/<name>/root/                    — jail root filesystem (mounted clone)
type Manager interface {
	// EnsureJail idempotently provisions the jail: release, ZFS datasets,
	// jail root population, jail.conf, and fstab.
	EnsureJail(ctx context.Context, j freebsdv1.Jail) error
	// DeleteJail stops the jail (if running) then tears down its ZFS datasets
	// and config files.
	DeleteJail(ctx context.Context, j freebsdv1.Jail) error
	// StartJail starts a provisioned jail via `jail -c`.
	StartJail(ctx context.Context, name string) error
	// StopJail stops a running jail via `jail -r`.
	StopJail(ctx context.Context, name string) error
	// RestartJail stops and then starts a jail.
	RestartJail(ctx context.Context, name string) error
	// IsRunning reports whether the named jail is currently active according
	// to jls(8).
	IsRunning(ctx context.Context, name string) (bool, error)
	// InstalledRelease returns the FreeBSD release string from the jail root
	// (reads /bin/freebsd-version).  Used to populate status.release and to
	// detect when spec.release differs from the provisioned base.
	InstalledRelease(jailRoot string) (string, error)
	// UpdateJail runs freebsd-update(8) against the jail root to apply
	// patch-level security updates within the current release.  The jail must
	// be stopped before calling this.
	UpdateJail(ctx context.Context, jailRoot string) error
	// ExecInJail runs a command inside a running jail via jexec(8).
	ExecInJail(ctx context.Context, jailName, command string, args ...string) error
	// BootstrapPkg installs the pkg(8) package manager inside the jail if
	// it is not already present.  The jail must be running.
	BootstrapPkg(ctx context.Context, jailName, jailRoot string) error
	// EnsureAnchor loads rules into a named PF anchor, fully replacing any
	// existing rules. An empty rules slice flushes the anchor instead.
	// The host pf.conf must contain `anchor "jails/*"` (or equivalent) for
	// the anchor to take effect; this method manages only the anchor itself.
	EnsureAnchor(ctx context.Context, anchorName string, rules []string) error
	// FlushAnchor removes all rules from a named PF anchor.
	FlushAnchor(ctx context.Context, anchorName string) error
}

var _ Manager = (*manager)(nil)

type manager struct {
	// basePath is the root filesystem path, e.g. /usr/local/nodemanager.
	basePath string
	// dataset is the root ZFS dataset name, e.g. zroot/nodemanager.
	dataset string
	// confDir is where per-jail .conf fragments are written.
	confDir string

	zfs      zfs.Manager
	exec     handler.ExecHandler
	releases ReleaseManager
}

// NewManager initialises the manager, ensuring the base ZFS dataset layout
// exists. It does not start or modify any jails.
func NewManager(ctx context.Context, basePath, zfsDataset, mirror string, exec handler.ExecHandler) (Manager, error) {
	zfsManager := zfs.NewZfsManager(exec)

	// Base dataset with explicit mountpoint.
	if err := zfsManager.Ensure(ctx, zfsDataset, "mountpoint="+basePath); err != nil {
		return nil, fmt.Errorf("ensuring base dataset %s: %w", zfsDataset, err)
	}

	// Child datasets inherit the mountpoint from the parent hierarchy.
	for _, sub := range []string{JailRootDir, ReleaseRootDir} {
		ds := filepath.Join(zfsDataset, sub)
		if err := zfsManager.Ensure(ctx, ds); err != nil {
			return nil, fmt.Errorf("ensuring dataset %s: %w", ds, err)
		}
	}

	releases := newReleaseManager(
		filepath.Join(basePath, ReleaseRootDir),
		filepath.Join(zfsDataset, ReleaseRootDir),
		mirror,
		zfsManager,
		exec,
	)

	return &manager{
		basePath: basePath,
		dataset:  zfsDataset,
		confDir:  DefaultJailConfDir,
		zfs:      zfsManager,
		exec:     exec,
		releases: releases,
	}, nil
}

// EnsureJail provisions the jail described by j. It is safe to call multiple
// times; each step is skipped when already in the desired state.
func (m *manager) EnsureJail(ctx context.Context, j freebsdv1.Jail) error {
	// 1. Ensure the FreeBSD release is downloaded and extracted.
	if err := m.releases.Ensure(ctx, j.Spec.Release); err != nil {
		return fmt.Errorf("ensuring release %s: %w", j.Spec.Release, err)
	}

	// 2. Ensure the per-jail container dataset exists (holds fstab, logs, etc.).
	jailDataset := filepath.Join(m.dataset, JailRootDir, j.Name)
	if err := m.zfs.Ensure(ctx, jailDataset); err != nil {
		return fmt.Errorf("ensuring jail dataset %s: %w", jailDataset, err)
	}

	// 3. Clone the release to the jail root if it does not already exist.
	//    This is a ZFS CoW clone, so it is nearly instantaneous and
	//    space-efficient until the jail diverges from the release.
	jailRootDataset := filepath.Join(m.dataset, JailRootDir, j.Name, "root")
	jailRoot := filepath.Join(m.basePath, JailRootDir, j.Name, "root")

	exists, err := m.zfs.Exists(ctx, jailRootDataset)
	if err != nil {
		return fmt.Errorf("checking jail root dataset: %w", err)
	}

	if exists {
		// Check whether the existing clone was built from spec.release.
		// Read origin before any destructive operation.
		origin, err := m.zfs.GetProperty(ctx, jailRootDataset, "origin")
		if err != nil {
			return fmt.Errorf("checking jail root origin for %s: %w", j.Name, err)
		}
		if origin != "" && origin != "-" && releaseFromOrigin(origin) != j.Spec.Release {
			// User changed spec.release — reprovision from the new base.
			_ = m.StopJail(ctx, j.Name)
			if err := m.zfs.DestroyDatasetRecursive(ctx, jailRootDataset); err != nil {
				return fmt.Errorf("destroying stale jail root for %s: %w", j.Name, err)
			}
			// Best-effort removal of the old release snapshot created for this jail.
			_ = m.zfs.DestroyDataset(ctx, origin)
			exists = false
		}
	}

	if !exists {
		releaseDataset := filepath.Join(m.dataset, ReleaseRootDir, j.Spec.Release)
		snapshot := fmt.Sprintf("%s@%s", releaseDataset, j.Name)

		snapshotExists, err := m.zfs.Exists(ctx, snapshot)
		if err != nil {
			return fmt.Errorf("checking release snapshot for %s: %w", j.Name, err)
		}
		if !snapshotExists {
			if err := m.zfs.Snapshot(ctx, releaseDataset, j.Name); err != nil {
				return fmt.Errorf("snapshotting release for %s: %w", j.Name, err)
			}
		}
		if err := m.zfs.Clone(ctx, snapshot, jailRootDataset, "mountpoint="+jailRoot); err != nil {
			return fmt.Errorf("cloning jail root for %s: %w", j.Name, err)
		}
	}

	// 4. Copy essential host files into the jail root.
	if err := m.copyHostFiles(jailRoot); err != nil {
		return fmt.Errorf("copying host files into jail %s: %w", j.Name, err)
	}

	// 5. Write per-jail fstab and ensure mountpoint directories exist.
	fstabPath := ""
	if len(j.Spec.Mounts) > 0 {
		fstabPath = filepath.Join(m.basePath, JailRootDir, j.Name, "fstab")
		if err := writeFstab(fstabPath, jailRoot, j.Spec.Mounts); err != nil {
			return fmt.Errorf("writing fstab for %s: %w", j.Name, err)
		}
		for _, mount := range j.Spec.Mounts {
			mp := filepath.Join(jailRoot, mount.JailPath)
			if err := os.MkdirAll(mp, 0o755); err != nil {
				return fmt.Errorf("creating mountpoint %s for jail %s: %w", mp, j.Name, err)
			}
		}
	}

	// 6. Write <confDir>/<name>.conf.
	if err := writeJailConf(m.confDir, j.Name, jailRoot, fstabPath, j.Spec); err != nil {
		return fmt.Errorf("writing jail.conf for %s: %w", j.Name, err)
	}

	// 7. Sync PF anchor when rules are declared.
	if j.Spec.PF != nil {
		anchor := jailAnchorName(j.Name, j.Spec.PF)
		if err := m.EnsureAnchor(ctx, anchor, j.Spec.PF.Rules); err != nil {
			return fmt.Errorf("syncing PF anchor %s for jail %s: %w", anchor, j.Name, err)
		}
	}

	// 8. Stop the jail if it is running with stale network config.  A running
	// jail ignores jail.conf changes; it must be cycled for the new IP to take
	// effect.  The controller's StartJail call will restart it on the next pass.
	if err := m.cycleJailIfNetworkChanged(ctx, j); err != nil {
		return fmt.Errorf("checking network change for %s: %w", j.Name, err)
	}

	return nil
}

// DeleteJail stops the jail (if running), then destroys its ZFS datasets and
// removes its config files. The release dataset and its snapshot are left in
// place for reuse by other jails on the same release.
func (m *manager) DeleteJail(ctx context.Context, j freebsdv1.Jail) error {
	// Stop the jail gracefully before tearing down its filesystem. Ignore
	// errors here — the jail may already be stopped.
	_ = m.StopJail(ctx, j.Name)

	// Remove IP aliases from the interface.  jail(8) removes these on a
	// clean stop; we repeat explicitly in case StopJail failed or the jail
	// was never fully started.
	m.removeIPAliases(ctx, j)

	// Flush the PF anchor regardless of whether PF is currently configured in
	// the spec — the user may have removed the field before deleting. This is
	// a best-effort cleanup; errors are logged but do not block deletion.
	anchor := jailAnchorName(j.Name, j.Spec.PF)
	_ = m.FlushAnchor(ctx, anchor)

	// Remove config files so the jail cannot be started accidentally
	// during teardown.
	if err := removeJailConf(m.confDir, j.Name); err != nil {
		return err
	}

	fstabPath := filepath.Join(m.basePath, JailRootDir, j.Name, "fstab")
	if err := removeFstab(fstabPath); err != nil {
		return err
	}

	jailRoot := filepath.Join(m.basePath, JailRootDir, j.Name, "root")

	// Explicitly unmount devfs (always at <root>/dev) and any nullfs mounts
	// declared in the spec.  jail(8) removes these on a clean stop, but they
	// linger when the jail was never fully started or jail -r returned an
	// error.  All calls are best-effort.
	_ = m.exec.SimpleRunCommand(ctx, "umount", "-f", filepath.Join(jailRoot, "dev"))
	for _, mnt := range j.Spec.Mounts {
		_ = m.exec.SimpleRunCommand(ctx, "umount", "-f", filepath.Join(jailRoot, mnt.JailPath))
	}

	// Scan for any remaining mounts not covered above and unmount them.
	m.unmountAll(ctx, jailRoot)

	// Force-unmount the jail root ZFS dataset before the recursive destroy.
	// The POSIX umount calls above handle devfs/nullfs children; the ZFS
	// dataset itself needs zfs-umount.  Error is ignored — the dataset may
	// already be unmounted or may not exist.
	jailRootDataset := filepath.Join(m.dataset, JailRootDir, j.Name, "root")
	_ = m.exec.SimpleRunCommand(ctx, "/sbin/zfs", "umount", "-f", jailRootDataset)

	// Recursively destroy jails/<name> and all child datasets (including root).
	jailDataset := filepath.Join(m.dataset, JailRootDir, j.Name)
	exists, err := m.zfs.Exists(ctx, jailDataset)
	if err != nil {
		return fmt.Errorf("checking jail dataset: %w", err)
	}
	if exists {
		if err := m.zfs.DestroyDatasetRecursive(ctx, jailDataset); err != nil {
			return fmt.Errorf("destroying jail dataset %s: %w", jailDataset, err)
		}
	}

	// Clean up the release snapshot created for this jail.
	if j.Spec.Release != "" {
		snapshot := fmt.Sprintf("%s/%s/%s@%s", m.dataset, ReleaseRootDir, j.Spec.Release, j.Name)
		snapshotExists, err := m.zfs.Exists(ctx, snapshot)
		if err != nil {
			return fmt.Errorf("checking release snapshot %s: %w", snapshot, err)
		}
		if snapshotExists {
			if err := m.zfs.DestroyDataset(ctx, snapshot); err != nil {
				return fmt.Errorf("destroying release snapshot %s: %w", snapshot, err)
			}
		}
	}

	return nil
}

func (m *manager) StartJail(ctx context.Context, name string) error {
	confPath := filepath.Join(m.confDir, name+".conf")
	return m.exec.SimpleRunCommand(ctx, "jail", "-c", "-f", confPath, name)
}

func (m *manager) StopJail(ctx context.Context, name string) error {
	return m.exec.SimpleRunCommand(ctx, "jail", "-r", name)
}

func (m *manager) RestartJail(ctx context.Context, name string) error {
	if err := m.StopJail(ctx, name); err != nil {
		return err
	}
	return m.StartJail(ctx, name)
}

func (m *manager) IsRunning(ctx context.Context, name string) (bool, error) {
	return isJailRunning(ctx, m.exec, name)
}

func (m *manager) InstalledRelease(jailRoot string) (string, error) {
	return installedRelease(jailRoot)
}

func (m *manager) ExecInJail(ctx context.Context, jailName, command string, args ...string) error {
	cmdArgs := append([]string{jailName, command}, args...)
	return m.exec.SimpleRunCommand(ctx, "jexec", cmdArgs...)
}

func (m *manager) BootstrapPkg(ctx context.Context, jailName, jailRoot string) error {
	pkgPath := filepath.Join(jailRoot, "usr/local/sbin/pkg")
	if _, err := os.Stat(pkgPath); err == nil {
		return nil
	}
	return m.ExecInJail(ctx, jailName, "env", "ASSUME_ALWAYS_YES=yes", "pkg", "bootstrap")
}

// UpdateJail runs freebsd-update(8) against the jail root to apply patch-level
// security updates.  The jail should be stopped before calling this.
func (m *manager) UpdateJail(ctx context.Context, jailRoot string) error {
	return m.exec.SimpleRunCommand(ctx, "env", "PAGER=cat",
		"/usr/sbin/freebsd-update", "-b", jailRoot, "--not-running-from-cron", "fetch", "install")
}

// jailAnchorName returns the PF anchor name for a jail. It uses the
// AnchorName from the PF spec when set, otherwise defaults to "jails/<name>".
func jailAnchorName(jailName string, pf *freebsdv1.JailPF) string {
	if pf != nil && pf.AnchorName != "" {
		return pf.AnchorName
	}
	return "jails/" + jailName
}

// EnsureAnchor loads rules into a named PF anchor via pfctl, replacing any
// existing rules atomically. Rules are written to pfctl's stdin to avoid
// temporary files, which matters on write-limited media such as SD cards.
// If rules is empty the anchor is flushed instead.
func (m *manager) EnsureAnchor(ctx context.Context, anchorName string, rules []string) error {
	if len(rules) == 0 {
		return m.FlushAnchor(ctx, anchorName)
	}
	input := strings.Join(rules, "\n") + "\n"
	_, _, err := m.exec.RunCommandWithInput(ctx, input, "pfctl", "-a", anchorName, "-f", "-")
	return err
}

// FlushAnchor removes all rules, NAT rules, and tables from a named PF anchor.
func (m *manager) FlushAnchor(ctx context.Context, anchorName string) error {
	return m.exec.SimpleRunCommand(ctx, "pfctl", "-a", anchorName, "-F", "all")
}

// releaseFromOrigin extracts the FreeBSD release component from a ZFS clone
// origin property.  Origin has the form:
//
//	<baseDataset>/releases/<version>@<jailName>
//
// e.g. "zroot/nodemanager/releases/14.2-RELEASE@web01" → "14.2-RELEASE".
func releaseFromOrigin(origin string) string {
	// Drop the @<jailName> snapshot suffix.
	datasetPart := strings.SplitN(origin, "@", 2)[0]
	// The last path component is the release version.
	return filepath.Base(datasetPart)
}

// installedRelease reads the USERLAND_VERSION from /bin/freebsd-version inside
// the jail root without executing any code — just string parsing.
func installedRelease(jailRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(jailRoot, "bin", "freebsd-version"))
	if err != nil {
		return "", fmt.Errorf("reading freebsd-version: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "USERLAND_VERSION=") {
			continue
		}
		v := strings.TrimPrefix(line, "USERLAND_VERSION=")
		v = strings.Trim(v, `"`)
		return v, nil
	}
	return "", fmt.Errorf("USERLAND_VERSION not found in %s/bin/freebsd-version", jailRoot)
}

// copyHostFiles copies /etc/resolv.conf and /etc/localtime into the jail root
// so that it has working DNS and correct timezone immediately after creation.
func (m *manager) copyHostFiles(jailRoot string) error {
	for _, f := range []string{"/etc/resolv.conf", "/etc/localtime"} {
		data, err := os.ReadFile(f)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("reading %s: %w", f, err)
		}
		dest := filepath.Join(jailRoot, f)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", dest, err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}
	return nil
}

// unmountAll scans for any filesystems mounted strictly under jailRoot
// (not jailRoot itself, which is a ZFS dataset handled by zfs-umount) and
// unmounts them deepest-first.  Each umount is best-effort — a single
// failure does not abort the rest, since the subsequent zfs destroy -r -f
// is the authoritative cleanup step.
func (m *manager) unmountAll(ctx context.Context, jailRoot string) {
	output, _, err := m.exec.RunCommand(ctx, "mount", "-p")
	if err != nil {
		return
	}

	prefix := jailRoot + "/"
	var mounts []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mp := fields[1]
		if strings.HasPrefix(mp, prefix) {
			mounts = append(mounts, mp)
		}
	}

	// Sort deepest-first to avoid "device busy" when a parent is still mounted.
	slices.SortFunc(mounts, func(a, b string) int {
		if len(a) > len(b) {
			return -1
		}
		if len(a) < len(b) {
			return 1
		}
		return strings.Compare(a, b)
	})

	for _, mp := range mounts {
		_ = m.exec.SimpleRunCommand(ctx, "umount", "-f", mp)
	}
}

// cycleJailIfNetworkChanged stops the jail when its running IPv4/IPv6
// addresses differ from spec, then removes any lingering IP aliases so
// jail -c can add them cleanly.  It also removes aliases when the jail is
// not running — aliases can be stranded after a crash or kill, causing
// jail -c to fail with "File exists".
// The function is a no-op when no network address is declared in spec.
func (m *manager) cycleJailIfNetworkChanged(ctx context.Context, j freebsdv1.Jail) error {
	if j.Spec.Inet == "" && j.Spec.Inet6 == "" {
		return nil
	}

	jails, err := listRunningJails(ctx, m.exec)
	if err != nil {
		return fmt.Errorf("listing running jails: %w", err)
	}

	for _, running := range jails {
		if running.Name != j.Name {
			continue
		}

		wantV4 := stripCIDR(j.Spec.Inet)
		wantV6 := stripCIDR(j.Spec.Inet6)

		gotV4 := ""
		if len(running.IPv4Addrs) > 0 {
			gotV4 = running.IPv4Addrs[0]
		}
		gotV6 := ""
		if len(running.IPv6Addrs) > 0 {
			gotV6 = running.IPv6Addrs[0]
		}

		if wantV4 == gotV4 && wantV6 == gotV6 {
			// Running with the correct config — nothing to do.
			return nil
		}

		// Config changed: stop so the controller restarts with the new conf.
		_ = m.StopJail(ctx, j.Name)
		break
	}

	// Jail is not running (never started, crashed, or we just stopped it).
	// Remove any orphaned IP aliases so jail -c can add them without error.
	m.removeIPAliases(ctx, j)
	return nil
}

// removeIPAliases removes the IPv4 and IPv6 aliases declared in spec from the
// interface.  Errors are intentionally ignored — the alias may not exist.
func (m *manager) removeIPAliases(ctx context.Context, j freebsdv1.Jail) {
	if j.Spec.Interface == "" {
		return
	}
	if j.Spec.Inet != "" {
		_ = m.exec.SimpleRunCommand(ctx, "ifconfig", j.Spec.Interface,
			"inet", stripCIDR(j.Spec.Inet), "-alias")
	}
	if j.Spec.Inet6 != "" {
		_ = m.exec.SimpleRunCommand(ctx, "ifconfig", j.Spec.Interface,
			"inet6", stripCIDR(j.Spec.Inet6), "-alias")
	}
}

// stripCIDR removes the prefix length from an IP address string, returning
// the bare IP.  Returns the input unchanged if no "/" is present.
func stripCIDR(addr string) string {
	return strings.SplitN(addr, "/", 2)[0]
}
