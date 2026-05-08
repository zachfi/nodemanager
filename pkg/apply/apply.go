// Package apply contains the OS-level apply logic for ConfigSet resources.
// It is used by both the cluster-connected controller (via the reconciler) and
// the offline "nodemanager apply" subcommand.
package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/packages"
	"github.com/zachfi/nodemanager/pkg/services"
	"github.com/zachfi/nodemanager/pkg/services/systemd"
)

// DataResolver is the interface for fetching data that may require Kubernetes
// access in cluster-connected mode. Offline implementations return
// locally-scoped data with warnings rather than errors.
type DataResolver interface {
	// CollectTemplateData returns the Data context used to render gomplate
	// templates declared in file. In offline mode, SecretRefs and ConfigMapRefs
	// are skipped; only Node.Labels, Node.Status, and the local node entry in
	// Nodes are populated.
	CollectTemplateData(ctx context.Context, namespace string, file commonv1.File, node commonv1.ManagedNode) (Data, error)

	// ManagedPathsUnder returns the set of file paths declared by any ConfigSet
	// that matches node, filtered to those whose cleaned path starts with
	// dirPath. Used to determine which files to retain when Purge is set.
	ManagedPathsUnder(ctx context.Context, namespace string, node commonv1.ManagedNode, dirPath string) map[string]struct{}
}

// NodeData carries per-node data available to gomplate templates.
type NodeData struct {
	Labels     map[string]string
	ConfigMaps map[string]string
	Secrets    map[string][]byte
	Status     commonv1.ManagedNodeStatus
}

// NodeInfo carries the identity and observed state of a single ManagedNode,
// available to templates via the Nodes field.
type NodeInfo struct {
	Name   string
	Labels map[string]string
	Status commonv1.ManagedNodeStatus
}

// Data is the template context passed to gomplate.
type Data struct {
	Node  NodeData
	Nodes []NodeInfo
}

// FileBucketConfig controls pre-write file backups.
type FileBucketConfig struct {
	Enabled          bool
	Path             string
	MaxFileSizeBytes int64
}

// Config holds Applier settings.
type Config struct {
	// GomplatePath is the path to the gomplate binary. Defaults to "gomplate".
	GomplatePath string
	// FileBucket controls optional pre-write backup of managed files.
	FileBucket FileBucketConfig
	// ManifestRoot is the base directory used to resolve relative SourceFile
	// paths. Empty means SourceFile paths are used as-is (absolute or relative
	// to the process working directory).
	ManifestRoot string
}

// Applier applies ConfigSet resources to the local OS.
type Applier struct {
	system handler.System
	logger *slog.Logger
	cfg    Config
}

// New returns a new Applier.
func New(system handler.System, logger *slog.Logger, cfg Config) *Applier {
	if cfg.GomplatePath == "" {
		cfg.GomplatePath = "gomplate"
	}
	return &Applier{system: system, logger: logger, cfg: cfg}
}

// Files applies the file declarations in fileSet to the local filesystem.
// It returns the list of changed paths and a path→backupHash map for files
// backed up to the filebucket.
func (a *Applier) Files(ctx context.Context, nodeName, configSetName, namespace string, fileSet []commonv1.File, node commonv1.ManagedNode, resolver DataResolver) (changedFiles []string, backupUpdates map[string]string, err error) {
	h := a.system.File()
	backupUpdates = make(map[string]string)
	var errs []error

	for _, file := range fileSet {
		switch files.FileEnsureFromString(file.Ensure) {
		case files.File:
			// Resolve SourceFile → Content/Template before template rendering.
			if file.SourceFile != "" && file.Content == "" && file.Template == "" {
				content, readErr := a.readSourceFile(file.SourceFile)
				if readErr != nil {
					errs = append(errs, fmt.Errorf("sourceFile %q for path %q: %w", file.SourceFile, file.Path, readErr))
					continue
				}
				if strings.HasSuffix(file.SourceFile, ".tmpl") {
					file.Template = string(content)
				} else {
					file.Content = string(content)
				}
			}

			if file.Template != "" {
				data, dataErr := resolver.CollectTemplateData(ctx, namespace, file, node)
				if dataErr != nil {
					errs = append(errs, dataErr)
					continue
				}
				rendered, tmplErr := a.buildTemplate(ctx, file.Template, data)
				if tmplErr != nil {
					errs = append(errs, fmt.Errorf("template for %q: %w", file.Path, tmplErr))
					continue
				}
				if len(rendered) > 0 {
					file.Content = string(rendered)
				}
			}

			if file.Content == "" || file.Ensure == files.Absent.String() {
				continue
			}

			if file.CreateOnly {
				if _, statErr := os.Stat(file.Path); statErr == nil {
					continue
				}
			}

			changed, backupHash, writeErr := a.writeFileContent(ctx, file, h)
			if writeErr != nil {
				errs = append(errs, writeErr)
				continue
			}
			if changed {
				changedFiles = append(changedFiles, file.Path)
			}
			if backupHash != "" {
				backupUpdates[file.Path] = backupHash
			}

		case files.Directory:
			if _, statErr := os.Stat(file.Path); os.IsNotExist(statErr) {
				mode := os.FileMode(0o660)
				if file.Mode != "" {
					var modeErr error
					mode, modeErr = files.GetFileModeFromString(ctx, file.Mode)
					if modeErr != nil {
						errs = append(errs, fmt.Errorf("mode for directory %q: %w", file.Path, modeErr))
						continue
					}
				}
				if mkdirErr := os.Mkdir(file.Path, mode); mkdirErr != nil {
					errs = append(errs, mkdirErr)
					continue
				}
				changedFiles = append(changedFiles, file.Path)
			} else if file.Mode != "" {
				changed, modeErr := h.SetMode(ctx, file.Path, file.Mode)
				if modeErr != nil {
					errs = append(errs, fmt.Errorf("set mode on %q: %w", file.Path, modeErr))
					continue
				}
				if changed {
					changedFiles = append(changedFiles, file.Path)
				}
			}

			if file.Purge {
				managed := resolver.ManagedPathsUnder(ctx, namespace, node, file.Path)
				dirEntries, readErr := os.ReadDir(file.Path)
				if readErr != nil {
					errs = append(errs, fmt.Errorf("purge: read %q: %w", file.Path, readErr))
					continue
				}
				for _, entry := range dirEntries {
					if entry.IsDir() {
						continue
					}
					entryPath := file.Path + "/" + entry.Name()
					if _, ok := managed[entryPath]; !ok {
						a.logger.Info("purging unmanaged file", "path", entryPath, "directory", file.Path)
						if removeErr := os.Remove(entryPath); removeErr != nil {
							errs = append(errs, fmt.Errorf("purge: remove %q: %w", entryPath, removeErr))
						} else {
							changedFiles = append(changedFiles, entryPath)
						}
					}
				}
			}

		case files.Symlink:
			// Lstat first so we can distinguish missing, regular file/dir, or
			// existing symlink. Calling Readlink unconditionally returned
			// EINVAL on a regular file and ENOENT on a missing path, which
			// blocked convergence forever — see configset_controller.go for
			// the full rationale.
			fi, statErr := os.Lstat(file.Path)
			switch {
			case errors.Is(statErr, os.ErrNotExist):
				if symlinkErr := os.Symlink(file.Target, file.Path); symlinkErr != nil {
					errs = append(errs, fmt.Errorf("symlink %q → %q: %w", file.Path, file.Target, symlinkErr))
					continue
				}
				changedFiles = append(changedFiles, file.Path)

			case statErr != nil:
				errs = append(errs, fmt.Errorf("stat %q: %w", file.Path, statErr))
				continue

			case fi.Mode()&os.ModeSymlink == 0:
				a.logger.Info("replacing non-symlink with symlink", "path", file.Path, "existing_mode", fi.Mode().String(), "target", file.Target)
				if _, removeErr := h.Remove(ctx, file.Path); removeErr != nil {
					errs = append(errs, fmt.Errorf("remove existing %q to install symlink: %w", file.Path, removeErr))
					continue
				}
				if symlinkErr := os.Symlink(file.Target, file.Path); symlinkErr != nil {
					errs = append(errs, fmt.Errorf("symlink %q → %q: %w", file.Path, file.Target, symlinkErr))
					continue
				}
				changedFiles = append(changedFiles, file.Path)

			default:
				target, linkErr := os.Readlink(file.Path)
				if linkErr != nil {
					errs = append(errs, fmt.Errorf("readlink %q: %w", file.Path, linkErr))
					continue
				}
				if target != file.Target {
					if changed, removeErr := h.Remove(ctx, file.Path); removeErr != nil {
						errs = append(errs, removeErr)
						continue
					} else if changed {
						changedFiles = append(changedFiles, file.Path)
					}
					if symlinkErr := os.Symlink(file.Target, file.Path); symlinkErr != nil {
						errs = append(errs, fmt.Errorf("symlink %q → %q: %w", file.Path, file.Target, symlinkErr))
					}
				}
			}

		case files.Absent:
			if changed, removeErr := h.Remove(ctx, file.Path); removeErr != nil {
				errs = append(errs, removeErr)
			} else if changed {
				changedFiles = append(changedFiles, file.Path)
			}

		default:
			errs = append(errs, fmt.Errorf("unhandled ensure %q for file %q", file.Ensure, file.Path))
		}
	}

	return changedFiles, backupUpdates, errors.Join(errs...)
}

// Packages installs or removes packages declared in packageSet.
func (a *Applier) Packages(ctx context.Context, packageSet []commonv1.Package) error {
	h := a.system.Package()
	pkgs, err := h.List(ctx)
	if err != nil {
		return err
	}

	var errs []error
	for _, pkg := range packageSet {
		switch packages.PackageEnsureFromString(pkg.Ensure) {
		case packages.Installed:
			installedVersion, installed := pkgs[pkg.Name]
			if !installed || (pkg.Version != "" && installedVersion != pkg.Version) {
				a.logger.Info("installing package", "name", pkg.Name, "version", pkg.Version)
				if installErr := h.Install(ctx, pkg.Name, pkg.Version); installErr != nil {
					errs = append(errs, installErr)
				}
			}
		case packages.Absent:
			if _, installed := pkgs[pkg.Name]; installed {
				a.logger.Info("removing package", "name", pkg.Name)
				if removeErr := h.Remove(ctx, pkg.Name); removeErr != nil {
					errs = append(errs, removeErr)
				}
			}
		default:
			errs = append(errs, fmt.Errorf("unhandled ensure %q for package %q", pkg.Ensure, pkg.Name))
		}
	}
	return errors.Join(errs...)
}

// Services ensures the services in serviceSet are in their desired state,
// restarting any that subscribe to a path in changedFiles.
func (a *Applier) Services(ctx context.Context, serviceSet []commonv1.Service, fileSet []commonv1.File, changedFiles []string) error {
	h := a.system.Service()

	type restartSvc struct {
		context.Context
		commonv1.Service
	}
	restartServices := make(map[string]restartSvc)
	for _, cf := range changedFiles {
		for _, svc := range serviceSet {
			if svc.Ensure != services.Running.String() {
				continue
			}
			for _, sub := range svc.SusbscribeFiles {
				if sub == cf {
					restartServices[svc.Name] = restartSvc{serviceContext(ctx, svc.User), svc}
				}
			}
		}
	}

	var errs []error
	for _, svc := range serviceSet {
		svcCtx := serviceContext(ctx, svc.User)
		svcH := withUserContext(h, svcCtx)

		rcConfPath := fmt.Sprintf("/etc/rc.conf.d/%s", svc.Name)
		rcFileManaged := false
		for _, f := range fileSet {
			if f.Path == rcConfPath {
				rcFileManaged = true
				break
			}
		}

		if !rcFileManaged {
			if svc.Enable {
				if enableErr := svcH.Enable(svcCtx, svc.Name); enableErr != nil {
					errs = append(errs, fmt.Errorf("enable %q: %w", svc.Name, enableErr))
				}
			} else {
				if disableErr := svcH.Disable(svcCtx, svc.Name); disableErr != nil {
					errs = append(errs, fmt.Errorf("disable %q: %w", svc.Name, disableErr))
				}
			}
			if svc.Arguments != "" {
				if argsErr := svcH.SetArguments(svcCtx, svc.Name, svc.Arguments); argsErr != nil {
					errs = append(errs, fmt.Errorf("set arguments for %q: %w", svc.Name, argsErr))
				}
			}
		}

		status, _ := svcH.Status(svcCtx, svc.Name)
		switch services.ServiceStatusFromString(svc.Ensure) {
		case services.Running:
			if status != services.Running {
				if startErr := svcH.Start(svcCtx, svc.Name); startErr != nil {
					errs = append(errs, fmt.Errorf("start %q: %w", svc.Name, startErr))
				}
			}
		case services.Stopped:
			if status != services.Stopped {
				if stopErr := svcH.Stop(svcCtx, svc.Name); stopErr != nil {
					errs = append(errs, fmt.Errorf("stop %q: %w", svc.Name, stopErr))
				}
			}
		}
	}

	for name, rs := range restartServices {
		a.logger.Info("restarting service", "name", name)
		rh := withUserContext(h, rs.Context)
		if restartErr := rh.Restart(rs.Context, name); restartErr != nil {
			errs = append(errs, fmt.Errorf("restart %q: %w", name, restartErr))
		}
	}

	return errors.Join(errs...)
}

// Execs runs executions that subscribe to a changed file.
func (a *Applier) Execs(ctx context.Context, execSet []commonv1.Exec, changedFiles []string) error {
	h := a.system.Exec()
	var errs []error
	seen := make(map[string]bool)
	for _, cf := range changedFiles {
		for _, exe := range execSet {
			for _, sub := range exe.SusbscribeFiles {
				if sub == cf && !seen[exe.Command] {
					seen[exe.Command] = true
					a.logger.Info("running exec", "command", exe.Command)
					if _, _, err := h.RunCommand(ctx, exe.Command, exe.Args...); err != nil {
						errs = append(errs, err)
					}
				}
			}
		}
	}
	return errors.Join(errs...)
}

func (a *Applier) readSourceFile(sourcePath string) ([]byte, error) {
	if a.cfg.ManifestRoot != "" && !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(a.cfg.ManifestRoot, sourcePath)
	}
	return os.ReadFile(sourcePath)
}

func (a *Applier) buildTemplate(ctx context.Context, template string, data Data) ([]byte, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, a.cfg.GomplatePath, "-i", template, "-d", "data=stdin:///data.json")
	cmd.Stdin = bytes.NewReader(b)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 10 * time.Second

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gomplate: %s: %w", stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

func (a *Applier) writeFileContent(ctx context.Context, file commonv1.File, h handler.FileHandler) (changed bool, backupHash string, err error) {
	if a.cfg.FileBucket.Enabled {
		info, statErr := os.Stat(file.Path)
		if statErr == nil && !info.IsDir() {
			if a.cfg.FileBucket.MaxFileSizeBytes > 0 && info.Size() > a.cfg.FileBucket.MaxFileSizeBytes {
				a.logger.Warn("skipping filebucket backup: file too large",
					"path", file.Path, "size", info.Size(), "limit", a.cfg.FileBucket.MaxFileSizeBytes)
			} else if current, readErr := os.ReadFile(file.Path); readErr == nil {
				if bh, bucketErr := files.SaveToFileBucket(a.cfg.FileBucket.Path, file.Path, current, info); bucketErr != nil {
					a.logger.Warn("filebucket backup failed", "path", file.Path, "err", bucketErr)
				} else {
					backupHash = bh
				}
			}
		}
	}

	contentChanged, err := h.WriteContentFile(ctx, file.Path, []byte(file.Content))
	if err != nil {
		return false, backupHash, fmt.Errorf("write %q: %w", file.Path, err)
	}

	ownerChanged, err := h.Chown(ctx, file.Path, file.Owner, file.Group)
	if err != nil {
		return true, backupHash, fmt.Errorf("chown %q: %w", file.Path, err)
	}

	modeChanged := false
	if file.Mode != "" {
		modeChanged, err = h.SetMode(ctx, file.Path, file.Mode)
		if err != nil {
			return true, backupHash, fmt.Errorf("chmod %q: %w", file.Path, err)
		}
	}

	return contentChanged || ownerChanged || modeChanged, backupHash, nil
}

func serviceContext(ctx context.Context, user string) context.Context {
	if user != "" {
		ctx = context.WithValue(ctx, systemd.UserContextKey, user)
	}
	return ctx
}

func withUserContext(h handler.ServiceHandler, ctx context.Context) handler.ServiceHandler {
	if s, ok := h.(*systemd.Systemd); ok {
		return s.WithContext(ctx)
	}
	return h
}
