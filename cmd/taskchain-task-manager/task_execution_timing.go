package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/executiontiming"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

var timingClock struct {
	once sync.Once
	id   string
	err  error
}

// Observation is opt-in and never changes the underlying command's result.
func runTaskWithTiming(args []string, out, errOut io.Writer) int {
	return observeTaskInvocation(args, out, errOut, runTask)
}

func observeTaskInvocation(args []string, out, errOut io.Writer, invoke func([]string, io.Writer, io.Writer) int) int {
	if os.Getenv("TASKCHAIN_EXECUTION_TIMING") != "1" || !timedInvocation(args) {
		return invoke(args, out, errOut)
	}
	context, contextErr := executiontiming.ParseContext([]byte(os.Getenv("TASKCHAIN_EXECUTION_TIMING_CONTEXT")))
	ledger, bindingErr := timingLedger()
	runID, identityErr := timingIdentity()
	timingClock.once.Do(func() { timingClock.id, timingClock.err = timingIdentity() })
	started := time.Now()
	code := invoke(args, out, errOut)
	finished := time.Now()
	duration := finished.Sub(started).Nanoseconds()
	if contextErr != nil || bindingErr != nil || identityErr != nil || timingClock.err != nil {
		cause := "identity unavailable"
		if contextErr != nil {
			cause = "invalid correlation context"
		} else if bindingErr != nil {
			cause = "Git binding unavailable"
		}
		fmt.Fprintln(errOut, "Execution timing unavailable:", cause)
		return code
	}
	status := "success"
	if code != 0 {
		status = "failure"
	}
	record := executiontiming.Record{
		Schema: executiontiming.SchemaV1, RunID: runID, StartedAt: started.UTC(), FinishedAt: finished.UTC(),
		Status: status, ExitCode: &code, DurationMonotonicNS: &duration, ClockDomainID: timingClock.id, Context: context,
	}
	if _, err := executiontiming.Publish(ledger, record); err != nil {
		// Do not print filesystem paths, raw context or underlying payloads.
		fmt.Fprintln(errOut, "Execution timing unavailable: sidecar publication failed")
	}
	return code
}

func timedInvocation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return false
		}
	}
	switch args[0] {
	case "run-doctor", "run-start", "run-status", "run-list", "run-finish", "run-recover", "run-abort", "run-close", "run-settle", "run-discard":
		return true
	}
	return false
}

func timingIdentity() (string, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(identity[:]), nil
}

func timingLedger() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	command := exec.Command("git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	common, err := command.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(common))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("nonabsolute Git common directory")
	}
	return filepath.Join(path, taskruntime.RuntimeStateDir, "task-runtime", "v1"), nil
}
