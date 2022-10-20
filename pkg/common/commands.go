package common

import (
	"bytes"
	"fmt"
	"os/exec"

	"github.com/pkg/errors"
)

func runCommand(command string, arg ...string) (string, int, error) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(command, arg...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return stderr.String(), cmd.ProcessState.ExitCode(), fmt.Errorf("failed to execute %q %s: %w", command, arg, err)
	}

	return out.String(), cmd.ProcessState.ExitCode(), nil
}

func simpleRunCommand(command string, arg ...string) error {
	out, _, err := runCommand(command, arg...)
	if err != nil {
		return errors.Wrap(err, out)
	}
	return err
}
