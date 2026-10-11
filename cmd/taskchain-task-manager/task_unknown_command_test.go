package main

import (
	"bytes"
	"fmt"
	"testing"
)

// unknownTaskCommandText is the spec's diagnostic for an unknown task
// subcommand, derived from taskCommandList rather than copied.
func unknownTaskCommandText(name string) string {
	return fmt.Sprintf("Unknown task command: %s\nAvailable: %s\n", name, taskCommandList)
}

func TestUnknownTaskCommandFollowsSpec(t *testing.T) {
	for _, name := range []string{"nope", "run-release", "issue-promote-all"} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := runTask([]string{name}, &out, &errOut)
			if code != 1 {
				t.Fatalf("exit=%d want 1", code)
			}
			if want := unknownTaskCommandText(name); errOut.String() != want {
				t.Fatalf("stderr=%q want %q", errOut.String(), want)
			}
			if out.Len() != 0 {
				t.Fatalf("stdout=%q want empty", out.String())
			}
		})
	}
}
