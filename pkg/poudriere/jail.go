package poudriere

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/zachfi/nodemanager/pkg/common"
	"go.opentelemetry.io/otel/trace"
)

type BuildJail struct {
	Name        string
	Version     string
	Arch        string
	FetchMethod string
	Creation    time.Time
	Path        string
}

// GetName implements nameable
func (j *BuildJail) GetName() string {
	return j.Name
}

type Jail interface {
	Create(BuildJail) error
	Delete(BuildJail) error
	List() ([]*BuildJail, error)
	Update(BuildJail) error
}

var _ Jail = &PoudriereJail{}

type PoudriereJail struct {
	tracer trace.Tracer
	logger *slog.Logger
}

func NewJail(logger *slog.Logger, tracer trace.Tracer) (*PoudriereJail, error) {
	return &PoudriereJail{
		logger: logger,
		tracer: tracer,
	}, nil
}

func (p *PoudriereJail) Create(jail BuildJail) error {
	return common.SimpleRunCommand(poudriere, "jail", "-c", "-j", jail.Name, "-v", jail.Version, "-m", "http")
}

func (p *PoudriereJail) Delete(jail BuildJail) error {
	return common.SimpleRunCommand(poudriere, "jail", "-d", "-j", jail.Name)
}

func (p *PoudriereJail) List() ([]*BuildJail, error) {
	out, _, err := common.RunCommand(poudriere, "jail", "-l")
	if err != nil {
		return nil, err
	}

	reader := bytes.NewReader([]byte(out))
	return p.readPoudriereJailList(reader)
}

func (p *PoudriereJail) Update(jail BuildJail) error {
	return common.SimpleRunCommand(poudriere, "jail", "-u", "-j", jail.Name)
}

func (p *PoudriereJail) readPoudriereJailList(r io.Reader) ([]*BuildJail, error) {
	var jails []*BuildJail

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)

		if len(parts) != 7 {
			p.logger.Info("skipping due to !=7 parts", "parts", len(parts))
			continue
		}
		d, t := parts[4], parts[5]
		created, err := time.Parse(time.DateTime, fmt.Sprintf("%s %s", d, t))
		if err != nil {
			return nil, err
		}

		jails = append(jails, &BuildJail{
			Name:        parts[0],
			Version:     parts[1],
			Arch:        parts[2],
			FetchMethod: parts[3],
			Path:        parts[6],
			Creation:    created,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return jails, nil
}
