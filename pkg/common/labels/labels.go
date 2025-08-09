package labels

import (
	"context"

	"github.com/zachfi/nodemanager/pkg/handler"
)

var (
	LabelOS        = "kubernetes.io/os"
	LabelArch      = "kubernetes.io/arch"
	LabelHostname  = "kubernetes.io/hostname"
	PoudriereBuild = "freebsd.nodemanager/poudriere"

	LabelUpgradeGroup = "upgrade.nodemanager/group"
)

type Logic int

const (
	Or Logic = iota
	And
	AnyKey
	NoneMatch
)

func DefaultLabels(ctx context.Context, h handler.NodeHandler) map[string]string {
	info := h.Info(ctx)

	return map[string]string{
		LabelOS:       info.OS.ID,
		LabelArch:     info.Machine,
		LabelHostname: info.Name,
	}
}

func LabelGate(logic Logic, labels map[string]string, dest map[string]string) bool {
	var matches int
	for key, value := range dest {
		for k, v := range labels {
			if k == key {
				switch logic {
				case AnyKey:
					return true
				}

				if v == value {
					matches++
					switch logic {
					case NoneMatch:
						return false
					case Or:
						return true
					}
				}
			}
		}
	}

	switch logic {
	case And:
		return matches == len(labels)
	case NoneMatch:
		if matches == 0 {
			return true
		}
	}

	return false
}
