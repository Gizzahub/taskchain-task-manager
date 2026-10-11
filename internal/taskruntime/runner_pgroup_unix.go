//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package taskruntime

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	// processGroupKillDelay is how long a cancelled group gets after SIGTERM
	// before SIGKILL. os/exec escalates only the leader, which would leave a
	// descendant that ignores SIGTERM running.
	processGroupKillDelay = 10 * time.Second
	// processGroupWaitDelay bounds Wait after cancellation so a descendant
	// holding the output pipes cannot hold the caller.
	processGroupWaitDelay = 30 * time.Second
)

// prepareProcessGroup makes cmd the leader of its own process group, so a
// cancelled context stops the whole tree: SIGTERM first, then SIGKILL.
func prepareProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid
		time.AfterFunc(processGroupKillDelay, func() { _ = syscall.Kill(pgid, syscall.SIGKILL) })
		return syscall.Kill(pgid, syscall.SIGTERM)
	}
	cmd.WaitDelay = processGroupWaitDelay
}

// forwardSignals relays SIGINT and SIGTERM to the group. Leaving the
// terminal's group means those signals no longer reach the tree on their own.
func forwardSignals(pid int) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-signals:
				if notified, ok := sig.(syscall.Signal); ok {
					_ = syscall.Kill(-pid, notified)
				}
			}
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}
