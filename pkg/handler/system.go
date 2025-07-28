package handler

type System interface {
	Package() PackageHandler
	Exec() ExecHandler
	Service() ServiceHandler
	File() FileHandler
	Node() NodeHandler
}
