package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/executiontiming"
)

func timingRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("TASKCHAIN_EXECUTION_TIMING", "1")
	t.Setenv("TASKCHAIN_EXECUTION_TIMING_CONTEXT", "")
	return filepath.Join(root, ".git", "ce", "task-runtime", "v1")
}

func TestExecutionTimingPreservesCommandBytesAndExit(t *testing.T) {
	for _, code := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			ledger := timingRepository(t)
			t.Setenv("TASKCHAIN_EXECUTION_TIMING_CONTEXT", `{"session_id":"session-a","tool_call_id":"call-a","relation_type":"launches"}`)
			var out, errOut bytes.Buffer
			calls := 0
			exit := observeTaskInvocation([]string{"run-list", "--json"}, &out, &errOut, func(_ []string, out, errOut io.Writer) int {
				calls++
				fmt.Fprint(out, "requested-data\n")
				fmt.Fprint(errOut, "original-diagnostic\n")
				return code
			})
			if exit != code || calls != 1 || out.String() != "requested-data\n" || errOut.String() != "original-diagnostic\n" {
				t.Fatalf("command changed: code=%d calls=%d stdout=%q stderr=%q", exit, calls, out.String(), errOut.String())
			}
			files, err := filepath.Glob(filepath.Join(ledger, "execution-timing", "v1", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("sidecars=%v err=%v", files, err)
			}
			body, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var record executiontiming.Record
			if err := json.Unmarshal(body, &record); err != nil {
				t.Fatal(err)
			}
			if record.ExitCode == nil || *record.ExitCode != code || record.DurationMonotonicNS == nil || *record.DurationMonotonicNS < 0 || record.ClockDomainID == "" || record.Context.ToolCallID != "call-a" {
				t.Fatalf("native boundary lost: %+v", record)
			}
			if strings.Contains(string(body), "requested-data") || strings.Contains(string(body), "original-diagnostic") || strings.Contains(string(body), ledger) {
				t.Fatal("raw payload leaked")
			}
		})
	}
}

func TestExecutionTimingFailureNeverMasksCommand(t *testing.T) {
	for _, kind := range []string{"invalid-context", "unsafe-sink"} {
		t.Run(kind, func(t *testing.T) {
			ledger := timingRepository(t)
			if kind == "invalid-context" {
				t.Setenv("TASKCHAIN_EXECUTION_TIMING_CONTEXT", `{"prompt":"secret-context"}`)
			} else {
				if err := os.MkdirAll(ledger, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(ledger, "execution-timing")); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut bytes.Buffer
			exit := observeTaskInvocation([]string{"run-status"}, &out, &errOut, func(_ []string, out, errOut io.Writer) int {
				fmt.Fprint(out, "original-data")
				fmt.Fprint(errOut, "original-error\n")
				return 3
			})
			if exit != 3 || out.String() != "original-data" || !strings.HasPrefix(errOut.String(), "original-error\nExecution timing unavailable:") {
				t.Fatalf("original result changed: %d %q %q", exit, out.String(), errOut.String())
			}
			if strings.Contains(errOut.String(), "secret-context") || strings.Contains(errOut.String(), ledger) {
				t.Fatal("diagnostic leaked raw data")
			}
		})
	}
}

func TestExecutionTimingOffAndHelpHaveNoSideEffects(t *testing.T) {
	for _, kind := range []string{"off", "help", "non-runtime"} {
		t.Run(kind, func(t *testing.T) {
			ledger := timingRepository(t)
			args := []string{"run-list"}
			switch kind {
			case "off":
				t.Setenv("TASKCHAIN_EXECUTION_TIMING", "")
			case "help":
				args = append(args, "--help")
			case "non-runtime":
				args = []string{"list"}
			}
			if code := observeTaskInvocation(args, io.Discard, io.Discard, func([]string, io.Writer, io.Writer) int { return 0 }); code != 0 {
				t.Fatal(code)
			}
			if _, err := os.Stat(ledger); !os.IsNotExist(err) {
				t.Fatalf("unexpected observation side effect: %v", err)
			}
		})
	}
}

func TestExecutionTimingRealCLIRefusalKeepsNativeOutput(t *testing.T) {
	timingRepository(t)
	args := []string{"task", "run-doctor", "--bogus"}
	var nativeOut, nativeErr, timedOut, timedErr bytes.Buffer
	t.Setenv("TASKCHAIN_EXECUTION_TIMING", "")
	native := run(args, &nativeOut, &nativeErr)
	t.Setenv("TASKCHAIN_EXECUTION_TIMING", "1")
	timed := run(args, &timedOut, &timedErr)
	if native != timed || nativeOut.String() != timedOut.String() || nativeErr.String() != timedErr.String() {
		t.Fatal("real command response changed")
	}
}
