package common

import (
	"context"

	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/services"
)

var systemHandler = &mockSystemHandler{}

var (
	_ handler.PackageHandler = (*mockPackageHandler)(nil)
	_ handler.ServiceHandler = (*mockServiceHandler)(nil)
	_ handler.FileHandler    = (*mockFileHandler)(nil)
	_ handler.ExecHandler    = (*mockExecHandler)(nil)
	_ handler.NodeHandler    = (*mockNodeHandler)(nil)
	_ handler.System         = (*mockSystemHandler)(nil)
)

type mockSystemHandler struct {
	packageHandler handler.PackageHandler
	serviceHandler handler.ServiceHandler
	fileHandler    handler.FileHandler
	nodeHandler    handler.NodeHandler
	execHandler    handler.ExecHandler
}

func (m *mockSystemHandler) Package() handler.PackageHandler {
	if m.packageHandler == nil {
		m.packageHandler = &mockPackageHandler{}
	}
	return m.packageHandler
}

func (m *mockSystemHandler) Service() handler.ServiceHandler {
	if m.serviceHandler == nil {
		m.serviceHandler = &mockServiceHandler{}
	}
	return m.serviceHandler
}

func (m *mockSystemHandler) File() handler.FileHandler {
	if m.fileHandler == nil {
		m.fileHandler = &mockFileHandler{}
	}
	return m.fileHandler
}

func (m *mockSystemHandler) Node() handler.NodeHandler {
	if m.nodeHandler == nil {
		m.nodeHandler = &mockNodeHandler{}
	}
	return m.nodeHandler
}

func (m *mockSystemHandler) Exec() handler.ExecHandler {
	if m.execHandler == nil {
		m.execHandler = &mockExecHandler{}
	}
	return m.execHandler
}

type mockServiceHandler struct {
	startCalls   map[string]int
	stopCalls    map[string]int
	restartCalls map[string]int
	statusCalls  map[string]int
	enableCalls  map[string]int
	disableCalls map[string]int
	reloadCalls  map[string]int
	setArgsCalls map[string]int

	// TODO: implement daemon reload calls if needed
	daemonReloadCalls int

	serviceStatus map[string]services.ServiceStatus // Simulated service status
}

func (m *mockServiceHandler) Start(ctx context.Context, service string) error {
	if m.startCalls == nil {
		m.startCalls = make(map[string]int)
	}
	m.startCalls[service]++
	// Simulate starting the service
	return nil // Return nil to indicate success
}

func (m *mockServiceHandler) Stop(ctx context.Context, service string) error {
	if m.stopCalls == nil {
		m.stopCalls = make(map[string]int)
	}
	m.stopCalls[service]++
	// Simulate stopping the service
	return nil // Return nil to indicate success
}

func (m *mockServiceHandler) Restart(ctx context.Context, service string) error {
	if m.restartCalls == nil {
		m.restartCalls = make(map[string]int)
	}
	m.restartCalls[service]++
	// Simulate restarting the service
	return nil // Return nil to indicate success
}

func (m *mockServiceHandler) Status(ctx context.Context, service string) (services.ServiceStatus, error) {
	if m.statusCalls == nil {
		m.statusCalls = make(map[string]int)
	}
	m.statusCalls[service]++

	if m.serviceStatus == nil {
		return services.Stopped, nil
	}

	status, exists := m.serviceStatus[service]
	if !exists {
		return services.UnknownServiceStatus, nil // Return unknown status if service not found
	}
	return status, nil
}

func (m *mockServiceHandler) Enable(ctx context.Context, service string) error {
	if m.enableCalls == nil {
		m.enableCalls = make(map[string]int)
	}
	m.enableCalls[service]++
	// Simulate enabling the service
	return nil // Return nil to indicate success
}

func (m *mockServiceHandler) Disable(ctx context.Context, service string) error {
	if m.disableCalls == nil {
		m.disableCalls = make(map[string]int)
	}
	m.disableCalls[service]++
	// Simulate disabling the service
	return nil // Return nil to indicate success
}

func (m *mockServiceHandler) SetArguments(ctx context.Context, service, args string) error {
	if m.setArgsCalls == nil {
		m.setArgsCalls = make(map[string]int)
	}
	m.setArgsCalls[service]++
	// Simulate setting service arguments
	return nil // Return nil to indicate success
}

// mockPackageHandler implements the PackageHandler interface for testing.
type mockPackageHandler struct {
	installCalls map[string]int
	removeCalls  map[string]int
	packageList  []string
	upgradeCalls int
}

func (m *mockPackageHandler) Install(ctx context.Context, pkg string) error {
	if m.installCalls == nil {
		m.installCalls = make(map[string]int)
	}
	m.installCalls[pkg]++

	return nil // Simulate successful installation
}

func (m *mockPackageHandler) Remove(ctx context.Context, pkg string) error {
	return nil // Simulate successful uninstallation
}

func (m *mockPackageHandler) List(ctx context.Context) ([]string, error) {
	if m.packageList == nil {
		m.packageList = []string{"pkg1", "pkg2", "pkg3"} // Simulate a list of installed packages
	}
	return m.packageList, nil // Return the simulated package list
}

func (m *mockPackageHandler) UpgradeAll(ctx context.Context) error {
	m.upgradeCalls++
	return nil // Simulate successful upgrade of all packages
}

// mockFileHandler implements the FileHandler interface for testing.
type mockFileHandler struct {
	fileExistsCalls map[string]int
	fileWriteCalls  map[string]int
	fileReadCalls   map[string]int
	fileRemoveCalls map[string]int
}

// type FileHandler interface {
// 	Chown(ctx context.Context, path, owner, group string) error
// 	SetMode(ctx context.Context, path, mode string) error
// 	WriteContentFile(ctx context.Context, path string, content []byte) error
// 	WriteTemplateFile(ctx context.Context, path, template string) error
// }

func (m *mockFileHandler) Chown(ctx context.Context, path, owner, group string) error {
	if m.fileExistsCalls == nil {
		m.fileExistsCalls = make(map[string]int)
	}
	m.fileExistsCalls[path]++

	// Simulate changing ownership
	return nil // Return nil to indicate success
}

func (m *mockFileHandler) SetMode(ctx context.Context, path, mode string) error {
	if m.fileWriteCalls == nil {
		m.fileWriteCalls = make(map[string]int)
	}
	m.fileWriteCalls[path]++

	// Simulate setting file mode
	return nil // Return nil to indicate success
}

func (m *mockFileHandler) WriteContentFile(ctx context.Context, path string, content []byte) error {
	if m.fileWriteCalls == nil {
		m.fileWriteCalls = make(map[string]int)
	}
	m.fileWriteCalls[path]++

	// TODO: record the content

	// Simulate writing content to a file
	return nil // Return nil to indicate success
}

func (m *mockFileHandler) Remove(ctx context.Context, path string) error {
	if m.fileRemoveCalls == nil {
		m.fileRemoveCalls = make(map[string]int)
	}
	m.fileRemoveCalls[path]++
	return nil // Simulate removing a file file
}

// mockNodeHandler implements the NodeHandler interface for testing.
type mockNodeHandler struct {
	rebootCalls  int
	upgradeCalls int
	hostname     string
	info         *handler.SysInfo
}

func (m *mockNodeHandler) Reboot(ctx context.Context) {
	m.rebootCalls++
}

func (m *mockNodeHandler) Upgrade(ctx context.Context) error {
	m.upgradeCalls++
	// Simulate an upgrade operation
	return nil // Return nil to indicate success
}

func (m *mockNodeHandler) Info(ctx context.Context) *handler.SysInfo {
	if m.info != nil {
		return m.info // Return the pre-set SysInfo if available
	}

	return &handler.SysInfo{
		Name:      "TestNode",
		Kernel:    "5.10.0",
		Version:   "1.0.0",
		Machine:   "x86_64",
		Domain:    "example.com",
		OS:        handler.OS{Release: "5.10", Name: "TestOS", ID: "testos"},
		Processor: "Intel Core i7",
		Runtime: struct {
			Arch string
			OS   string
		}{
			Arch: "amd64",
			OS:   "linux",
		},
	}
}

func (m *mockNodeHandler) Hostname() (string, error) {
	if m.hostname == "" {
		m.hostname = "test-node" // Simulate a hostname if not set
	}
	return m.hostname, nil // Return the simulated hostname
}

// mockExecHandler implements the ExecHandler interface for testing.
type mockExecHandler struct {
	execCalls map[string]int
}

func (m *mockExecHandler) RunCommand(ctx context.Context, command string, arg ...string) (string, int, error) {
	if m.execCalls == nil {
		m.execCalls = make(map[string]int)
	}
	// TODO: record the command and its arguments

	m.execCalls[command]++

	// Simulate command execution
	return "output", 0, nil // Return simulated output and exit code
}

func (m *mockExecHandler) SimpleRunCommand(ctx context.Context, command string, arg ...string) error {
	_, _, err := m.RunCommand(ctx, command, arg...)
	return err
}
