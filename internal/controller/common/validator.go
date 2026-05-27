/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package common

import (
	"context"
	"strings"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// substitute applies the ${STAGED} / ${TARGET} token replacement to a
// validator command and its args. Unchanged if neither token appears.
func substitute(cmd string, args []string, staged, target string) (string, []string) {
	rep := strings.NewReplacer("${STAGED}", staged, "${TARGET}", target)
	var out []string
	if args != nil {
		out = make([]string, len(args))
		for i, a := range args {
			out[i] = rep.Replace(a)
		}
	}
	return rep.Replace(cmd), out
}

// lastKB returns at most the last 1024 bytes of s. Used to bound validator
// stderr in log lines so a runaway validator can't blow up the log shipper.
func lastKB(s string) string {
	const limit = 1024
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

// runValidator executes v with a per-call timeout derived from v.Timeout().
// Returns the captured output (stderr on failure, stdout on success — see
// the underlying ExecHandler contract), exit code, and any execution error.
func runValidator(ctx context.Context, execer handler.ExecHandler, v *commonv1.Validate, staged, target string) (output string, exit int, err error) {
	ctx, cancel := context.WithTimeout(ctx, v.Timeout())
	defer cancel()
	command, args := substitute(v.Command, v.Args, staged, target)
	return execer.RunCommand(ctx, command, args...)
}
