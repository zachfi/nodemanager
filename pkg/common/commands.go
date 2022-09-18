package common

import (
	"bytes"
	"fmt"
	"os/exec"
)

func runCommand(command string, arg ...string) (string, int, error) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(command, arg...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return out.String(), cmd.ProcessState.ExitCode(), fmt.Errorf("failed to execute %q: %w", command, err)
	}

	return out.String(), cmd.ProcessState.ExitCode(), nil
}

func simpleRunCommand(command string, arg ...string) error {
	_, _, err := runCommand(command, arg...)
	return err
}
