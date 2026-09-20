package taskstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContextRegisterProcessHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("TASKSTORE_CONTEXT_HELPER") != "1" {
		return
	}
	board, title := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	raw := []byte(fmt.Sprintf(`{"schemaVersion":1,"kind":"intent","id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"title":%q,"outcome":"Publish evidence","mode":"completion","constraints":[],"nonGoals":[],"successCriteria":[{"key":"published","text":"Evidence exists"}]}`, title))
	if phase := os.Getenv("TASKSTORE_CONTEXT_CRASH"); phase != "" {
		_, err := registerContextWithStep(board, raw, func(at string) error {
			if at == phase {
				os.Exit(7)
			}
			return nil
		})
		fmt.Fprint(os.Stderr, err)
		os.Exit(8)
	}
	for attempt := 0; attempt < 100; attempt++ {
		result, err := RegisterContext(board, raw)
		if err == nil {
			fmt.Print(result.Status)
			os.Exit(0)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "locked") {
			fmt.Fprint(os.Stderr, err)
			os.Exit(2)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Fprint(os.Stderr, "lock retry budget exhausted")
	os.Exit(3)
}

func TestContextRegistryProcessExitPreservesCommitPoint(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-stage", "after-link"} {
		t.Run(phase, func(t *testing.T) {
			board := claimBoard(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestContextRegisterProcessHelper$", "--", board, "same content")
			cmd.Env = append(os.Environ(), "TASKSTORE_CONTEXT_HELPER=1", "TASKSTORE_CONTEXT_CRASH="+phase)
			output, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 {
				t.Fatalf("crash not reached: %v %s", err, output)
			}
			id := "INTENT-0123456789abcdef0123456789abcdef"
			_, err = ShowContext(board, "intent", id, 1)
			assertLocked(t, err)
			// The child is authoritatively terminal. Only the empty lock created
			// by this fixture is removed; the product never removes it itself.
			if err := os.Remove(filepath.Join(board, ".task-manager.lock")); err != nil {
				t.Fatal(err)
			}
			_, err = ShowContext(board, "intent", id, 1)
			if phase == "after-stage" && (err == nil || !strings.Contains(err.Error(), "not found")) {
				t.Fatalf("unpublished document: %v", err)
			}
			if phase == "after-link" && err != nil {
				t.Fatal(err)
			}
			cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestContextRegisterProcessHelper$", "--", board, "same content")
			cmd.Env = append(os.Environ(), "TASKSTORE_CONTEXT_HELPER=1")
			output, err = cmd.CombinedOutput()
			want := "registered"
			if phase == "after-link" {
				want = "unchanged"
			}
			if err != nil || strings.TrimSpace(string(output)) != want {
				t.Fatalf("retry=%s error=%v", output, err)
			}
		})
	}
}

func TestContextRegisterConcurrentProcesses(t *testing.T) {
	t.Parallel()
	for _, distinct := range []bool{false, true} {
		t.Run(fmt.Sprintf("distinct-content-%v", distinct), func(t *testing.T) {
			board := claimBoard(t)
			type outcome struct {
				output string
				err    error
			}
			results := make(chan outcome, 2)
			for i := 0; i < 2; i++ {
				title := "same content"
				if distinct && i == 1 {
					title = "different content"
				}
				go func(title string) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestContextRegisterProcessHelper$", "--", board, title)
					cmd.Env = append(os.Environ(), "TASKSTORE_CONTEXT_HELPER=1")
					raw, err := cmd.CombinedOutput()
					results <- outcome{strings.TrimSpace(string(raw)), err}
				}(title)
			}
			registered, unchanged, conflicts := 0, 0, 0
			for i := 0; i < 2; i++ {
				result := <-results
				switch {
				case result.err == nil && result.output == "registered":
					registered++
				case result.err == nil && result.output == "unchanged":
					unchanged++
				case result.err != nil && strings.Contains(result.output, "different immutable content"):
					conflicts++
				default:
					t.Fatalf("unexpected subprocess result: %+v", result)
				}
			}
			if registered != 1 || (!distinct && unchanged != 1) || (distinct && conflicts != 1) {
				t.Fatalf("registered=%d unchanged=%d conflicts=%d", registered, unchanged, conflicts)
			}
			if _, err := ShowContext(board, "intent", "INTENT-0123456789abcdef0123456789abcdef", 1); err != nil {
				t.Fatal(err)
			}
		})
	}
}
