package common

var (
	LabelOS       = "kubernetes.io/os"
	LabelArch     = "kubernetes.io/arch"
	LabelHostname = "kubernetes.io/hostname"
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
