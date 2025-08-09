package handler

import "context"

type PackageHandler interface {
	Install(context.Context, string) error
	Remove(context.Context, string) error
	List(context.Context) ([]string, error)
	UpgradeAll(context.Context) error
}
