package handler

import "context"

type PackageHandler interface {
	// Install installs the named package. If version is non-empty, the exact
	// version is requested; otherwise the latest available version is used.
	Install(ctx context.Context, name, version string) error
	Remove(context.Context, string) error
	// List returns a map of installed package names to their installed versions.
	List(context.Context) (map[string]string, error)
	UpgradeAll(context.Context) error
}
