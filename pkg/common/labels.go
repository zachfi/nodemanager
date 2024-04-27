package common

var (
	LabelOS        = "kubernetes.io/os"
	LabelArch      = "kubernetes.io/arch"
	LabelHostname  = "kubernetes.io/hostname"
	PoudriereBuild = "freebsd.nodemanager/poudriere"
)

type Logic int

const (
	Or Logic = iota
	And
	AnyKey
	NoneMatch
)

func DefaultLabels() map[string]string {
	resolver := &UnameInfoResolver{}
	info := resolver.Info()

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
			if k == key && logic == AnyKey {
				return true
			}

			if k == key && v == value {
				matches++
				if logic == Or {
					return true
				}
			} else {
				if logic == And {
					return false
				}
			}
		}
	}

	switch logic {
	case NoneMatch:
		if matches > 0 {
			return false
		}
	}

	return matches == len(dest)
}
