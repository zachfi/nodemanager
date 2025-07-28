package packages

type PackageEnsure int64

const (
	UnhandledPackageEnsure PackageEnsure = iota
	Installed
	Absent
)

var EnsureByName map[string]PackageEnsure = map[string]PackageEnsure{
	"unhandled": UnhandledPackageEnsure,
	"installed": Installed,
	"absent":    Absent,
}

func (p PackageEnsure) String() string {
	switch p {
	case UnhandledPackageEnsure:
		return "unhandled"
	case Installed:
		return "installed"
	case Absent:
		return "absent"
	}
	return "unhandled"
}

func PackageEnsureFromString(ensure string) PackageEnsure {
	if p, ok := EnsureByName[ensure]; ok {
		return p
	}
	return UnhandledPackageEnsure
}
