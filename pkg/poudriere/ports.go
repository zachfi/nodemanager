package poudriere

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/zachfi/nodemanager/pkg/handler"
)

type PortsTree struct {
	Name         string
	FetchMethod  string
	Branch       string
	CreationDate string
	CreationTime string
	Mountpoint   string
}

// GetName implements nameable
func (p *PortsTree) GetName() string {
	return p.Name
}

// Dependencies:
// - poudriere

type Ports interface {
	Create(context.Context, PortsTree) error
	Delete(context.Context, PortsTree) error
	List(context.Context) ([]*PortsTree, error)
	Update(context.Context, PortsTree) error
}

var _ Ports = (*PoudrierePorts)(nil)

const (
	poudriere  = "/usr/local/bin/poudriere"
	portshaker = "/usr/local/bin/portshaker"
)

type PoudrierePorts struct {
	logger *slog.Logger

	exec handler.ExecHandler
}

func NewPorts(logger *slog.Logger, exec handler.ExecHandler) (*PoudrierePorts, error) {
	return &PoudrierePorts{
		logger: logger,
		exec:   exec,
	}, nil
}

func (p *PoudrierePorts) Create(ctx context.Context, tree PortsTree) error {
	return p.exec.SimpleRunCommand(ctx, poudriere, "ports", "-c", "-p", tree.Name, "-m", tree.FetchMethod)
}

func (p *PoudrierePorts) Delete(ctx context.Context, tree PortsTree) error {
	return p.exec.SimpleRunCommand(ctx, poudriere, "ports", "-d", "-p", tree.Name)
}

func (p *PoudrierePorts) List(ctx context.Context) ([]*PortsTree, error) {
	out, _, err := p.exec.RunCommand(ctx, poudriere, "ports", "-l")
	if err != nil {
		return nil, err
	}

	reader := bytes.NewReader([]byte(out))
	return p.readPoudrierePortsStatus(reader)
}

func (p *PoudrierePorts) Update(ctx context.Context, tree PortsTree) error {
	return nil
}

func (p *PoudrierePorts) readPoudrierePortsStatus(r io.Reader) ([]*PortsTree, error) {
	var trees []*PortsTree

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)

		if len(parts) != 5 {
			p.logger.Info("skipping due to !=5 parts", "parts", len(parts))
			continue
		}

		trees = append(trees, &PortsTree{
			Name:         parts[0],
			FetchMethod:  parts[1],
			CreationDate: parts[2],
			CreationTime: parts[3],
			Mountpoint:   parts[4],
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return trees, nil
}
