package poudriere

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/zachfi/nodemanager/pkg/handler"
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
	Create(context.Context, BuildJail) error
	Delete(context.Context, BuildJail) error
	List(context.Context) ([]*BuildJail, error)
	Update(context.Context, BuildJail) error
}

var _ Jail = (*PoudriereJail)(nil)

type PoudriereJail struct {
	logger *slog.Logger

	exec handler.ExecHandler
}

func NewJail(logger *slog.Logger, exec handler.ExecHandler) (*PoudriereJail, error) {
	return &PoudriereJail{
		logger: logger,
		exec:   exec,
	}, nil
}

func (p *PoudriereJail) Create(ctx context.Context, jail BuildJail) error {
	return p.exec.SimpleRunCommand(ctx, poudriere, "jail", "-c", "-j", jail.Name, "-v", jail.Version, "-m", "http")
}

func (p *PoudriereJail) Delete(ctx context.Context, jail BuildJail) error {
	return p.exec.SimpleRunCommand(ctx, poudriere, "jail", "-d", "-j", jail.Name)
}

func (p *PoudriereJail) List(ctx context.Context) ([]*BuildJail, error) {
	out, _, err := p.exec.RunCommand(ctx, poudriere, "jail", "-l")
	if err != nil {
		return nil, err
	}

	reader := bytes.NewReader([]byte(out))
	return p.readPoudriereJailList(reader)
}

func (p *PoudriereJail) Update(ctx context.Context, jail BuildJail) error {
	return p.exec.SimpleRunCommand(ctx, poudriere, "jail", "-u", "-j", jail.Name)
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
