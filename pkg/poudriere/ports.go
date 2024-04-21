package poudriere

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/nodemanager/pkg/common"
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
	Create(PortsTree) error
	Delete(PortsTree) error
	List() ([]*PortsTree, error)
	Update(PortsTree) error
}

var _ Ports = &PoudrierePorts{}

const (
	poudriere = "/usr/local/bin/poudriere"
)

type PoudrierePorts struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func NewPorts(logger *slog.Logger, tracer trace.Tracer) (*PoudrierePorts, error) {
	return &PoudrierePorts{
		logger: logger,
		tracer: tracer,
	}, nil
}

func (p *PoudrierePorts) Create(tree PortsTree) error {
	return common.SimpleRunCommand(poudriere, "ports", "-c", "-p", tree.Name, "-m", tree.FetchMethod)
}

func (p *PoudrierePorts) Delete(tree PortsTree) error {
	return common.SimpleRunCommand(poudriere, "ports", "-d", "-p", tree.Name)
}

func (p *PoudrierePorts) List() ([]*PortsTree, error) {
	out, _, err := common.RunCommand(poudriere, "ports", "-l")
	if err != nil {
		return nil, err
	}

	reader := bytes.NewReader([]byte(out))
	return p.readPoudrierePortsStatus(reader)
}

func (p *PoudrierePorts) Update(tree PortsTree) error {
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
