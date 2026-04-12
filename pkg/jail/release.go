package jail

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/zfs"
)

// goarchToFreeBSD maps Go GOARCH values to the FreeBSD mirror path segment
// used in release URLs: <machine>/<processor>.
var goarchToFreeBSD = map[string][2]string{
	"amd64": {"amd64", "amd64"},
	"arm64": {"arm64", "aarch64"},
	"arm":   {"arm", "armv7"},
}

// ReleaseManager downloads and extracts FreeBSD base releases for use as jail
// templates.
type ReleaseManager interface {
	// Ensure downloads and extracts the given release if not already present.
	Ensure(ctx context.Context, version string) error
	// Path returns the filesystem path of the extracted release root.
	Path(version string) string
}

type releaseManager struct {
	// basePath is the filesystem root for extracted releases,
	// e.g. /usr/local/nodemanager/releases.
	basePath string
	// dataset is the ZFS dataset path for releases,
	// e.g. zroot/nodemanager/releases.
	dataset string
	// mirror is the FreeBSD mirror base URL,
	// e.g. "https://download.freebsd.org/releases".
	mirror string
	// arch is the FreeBSD mirror path segment, e.g. "amd64/amd64".
	arch string

	zfs        zfs.Manager
	exec       handler.ExecHandler
	httpClient *http.Client
}

func newReleaseManager(basePath, dataset, mirror string, zfsManager zfs.Manager, exec handler.ExecHandler) ReleaseManager {
	machine, processor := freebsdArch()
	return &releaseManager{
		basePath:   basePath,
		dataset:    dataset,
		mirror:     mirror,
		arch:       machine + "/" + processor,
		zfs:        zfsManager,
		exec:       exec,
		httpClient: http.DefaultClient,
	}
}

// Ensure makes the release available at Path(version). It is idempotent: if
// bin/freebsd-version already exists inside the release root the function
// returns immediately.
func (r *releaseManager) Ensure(ctx context.Context, version string) error {
	// Ensure the ZFS dataset for this release exists.
	releaseDataset := filepath.Join(r.dataset, version)
	if err := r.zfs.Ensure(ctx, releaseDataset); err != nil {
		return fmt.Errorf("ensuring release dataset %s: %w", releaseDataset, err)
	}

	root := r.Path(version)

	// Short-circuit: release already extracted.
	if r.isExtracted(root) {
		return nil
	}

	// Download base.txz to a temporary file.
	url := fmt.Sprintf("%s/%s/%s/base.txz", r.mirror, r.arch, version)
	tmp, err := os.CreateTemp("", "nodemanager-base-*.txz")
	if err != nil {
		return fmt.Errorf("creating temp file for release download: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := r.download(ctx, url, tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	tmp.Close()

	// Extract into the release root.
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("creating release root %s: %w", root, err)
	}
	if err := r.extract(ctx, tmpName, root); err != nil {
		return fmt.Errorf("extracting release %s: %w", version, err)
	}

	return nil
}

func (r *releaseManager) Path(version string) string {
	return filepath.Join(r.basePath, version)
}

// isExtracted returns true when the release root contains a FreeBSD userland.
func (r *releaseManager) isExtracted(root string) bool {
	_, err := os.Stat(filepath.Join(root, "bin", "freebsd-version"))
	return err == nil
}

func (r *releaseManager) download(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP %d fetching %s", resp.StatusCode, url)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("writing download: %w", err)
	}
	return nil
}

// extract unpacks txzPath into dest using bsdtar.
func (r *releaseManager) extract(ctx context.Context, txzPath, dest string) error {
	return r.exec.SimpleRunCommand(ctx, "tar", "-xf", txzPath, "--unlink", "-C", dest)
}

// freebsdArch returns the (machine, processor) strings used in FreeBSD mirror
// URLs, derived from the compiled GOARCH.
func freebsdArch() (machine, processor string) {
	if pair, ok := goarchToFreeBSD[runtime.GOARCH]; ok {
		return pair[0], pair[1]
	}
	// Fallback: use GOARCH for both fields; will likely produce a 404 on
	// unsupported arches, which is an honest error.
	return runtime.GOARCH, runtime.GOARCH
}
