//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package taskruntime

import "os/exec"

// prepareProcessGroup has no process-group primitive here. Cancellation stops
// only the direct child, as for any other command.
func prepareProcessGroup(*exec.Cmd) {}

func forwardSignals(int) func() { return func() {} }
