/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package common

import (
	"strings"
	"testing"
)

func TestSubstitute(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		args     []string
		staged   string
		target   string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "no tokens",
			cmd:      "nsd-checkzone",
			args:     []string{"znet", "/etc/nsd/znet.zone"},
			staged:   "/tmp/x.staged",
			target:   "/etc/nsd/znet.zone",
			wantCmd:  "nsd-checkzone",
			wantArgs: []string{"znet", "/etc/nsd/znet.zone"},
		},
		{
			name:     "STAGED in args",
			cmd:      "nsd-checkzone",
			args:     []string{"znet", "${STAGED}"},
			staged:   "/etc/nsd/znet.zone.nm-staged-Abc",
			target:   "/etc/nsd/znet.zone",
			wantCmd:  "nsd-checkzone",
			wantArgs: []string{"znet", "/etc/nsd/znet.zone.nm-staged-Abc"},
		},
		{
			name:     "TARGET in args",
			cmd:      "diff",
			args:     []string{"-u", "${TARGET}", "${STAGED}"},
			staged:   "/tmp/x.staged",
			target:   "/etc/x",
			wantCmd:  "diff",
			wantArgs: []string{"-u", "/etc/x", "/tmp/x.staged"},
		},
		{
			name:     "tokens in command",
			cmd:      "${STAGED}",
			args:     nil,
			staged:   "/tmp/checker",
			target:   "/etc/x",
			wantCmd:  "/tmp/checker",
			wantArgs: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCmd, gotArgs := substitute(tc.cmd, tc.args, tc.staged, tc.target)
			if gotCmd != tc.wantCmd {
				t.Errorf("cmd: got %q, want %q", gotCmd, tc.wantCmd)
			}
			if len(gotArgs) != len(tc.wantArgs) {
				t.Fatalf("args length: got %d, want %d", len(gotArgs), len(tc.wantArgs))
			}
			for i := range gotArgs {
				if gotArgs[i] != tc.wantArgs[i] {
					t.Errorf("args[%d]: got %q, want %q", i, gotArgs[i], tc.wantArgs[i])
				}
			}
		})
	}
}

func TestLastKB(t *testing.T) {
	short := "validator failed: line 3\n"
	if got := lastKB(short); got != short {
		t.Errorf("short input should pass through; got %q want %q", got, short)
	}

	long := strings.Repeat("abcdefghij", 200) // 2000 bytes
	got := lastKB(long)
	if len(got) > 1024 {
		t.Errorf("output exceeds 1KB: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, long[len(long)-512:]) {
		t.Errorf("output does not end with the tail of the input")
	}
}
