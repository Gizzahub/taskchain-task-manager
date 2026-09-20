package taskstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func TestContextIterationRegisterProcessHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("TASKSTORE_ITERATION_HELPER") != "1" {
		return
	}
	board := os.Args[len(os.Args)-2]
	variant := os.Args[len(os.Args)-1]
	rawIntent, intent := maintenanceIntentRaw(t)
	for attempt := 0; attempt < 100; attempt++ {
		if _, err := RegisterContext(board, rawIntent); err == nil {
			break
		} else if !strings.Contains(strings.ToLower(err.Error()), "locked") {
			fmt.Fprint(os.Stderr, err)
			os.Exit(2)
		} else if attempt == 99 {
			fmt.Fprint(os.Stderr, "lock retry budget exhausted")
			os.Exit(3)
		}
		time.Sleep(20 * time.Millisecond)
	}
	id := "ITERATION-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	raw := maintenanceIterationRaw(t, intent, id, "idle", nil, nil)
	if variant == "different" {
		changed := []byte(strings.Replace(string(raw), `"reason":"observed"`, `"reason":"different"`, 1))
		if bytes.Equal(raw, changed) {
			t.Fatal("different contender mutation missed its target")
		}
		raw = changed
	}
	if phase := os.Getenv("TASKSTORE_ITERATION_CRASH"); phase != "" {
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

func runIterationProcess(t *testing.T, board, variant, phase string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestContextIterationRegisterProcessHelper$", "--", board, variant)
	cmd.Env = append(os.Environ(), "TASKSTORE_ITERATION_HELPER=1")
	if phase != "" {
		cmd.Env = append(cmd.Env, "TASKSTORE_ITERATION_CRASH="+phase)
	}
	return cmd.CombinedOutput()
}

func TestContextIterationProcessExitPreservesCommitPoint(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-stage", "after-link"} {
		t.Run(phase, func(t *testing.T) {
			board := filepath.Join(t.TempDir(), "tasks")
			if err := Init(board); err != nil {
				t.Fatal(err)
			}
			output, err := runIterationProcess(t, board, "same", phase)
			if err == nil {
				t.Fatalf("crash not reached: %v %s", err, output)
			}
			if cmdErr, ok := err.(*exec.ExitError); !ok || cmdErr.ExitCode() != 7 {
				t.Fatalf("crash exit=%v output=%s", err, output)
			}
			if err := os.Remove(filepath.Join(board, ".task-manager.lock")); err != nil {
				t.Fatal(err)
			}
			_, intent := maintenanceIntentRaw(t)
			wantIntent, err := intent.Canonical()
			if err != nil {
				t.Fatal(err)
			}
			shownIntent, err := ShowContext(board, "intent", maintenanceIntentID, 1)
			if err != nil || !bytes.Equal(shownIntent.Canonical, wantIntent) {
				t.Fatalf("intent dependency changed: %+v err=%v", shownIntent, err)
			}
			id := "ITERATION-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			_, showErr := ShowContext(board, "iteration", id, 1)
			if phase == "after-stage" && (showErr == nil || !strings.Contains(showErr.Error(), "not found")) {
				t.Fatalf("unpublished iteration=%v", showErr)
			}
			if phase == "after-link" && showErr != nil {
				t.Fatalf("published iteration missing: %v", showErr)
			}
			if phase == "after-link" {
				wantIteration := maintenanceIterationRaw(t, intent, id, "idle", nil, nil)
				parsed, err := intentdoc.Parse(wantIteration)
				if err != nil {
					t.Fatal(err)
				}
				wantCanonical, _ := parsed.Canonical()
				shown, err := ShowContext(board, "iteration", id, 1)
				if err != nil || !bytes.Equal(shown.Canonical, wantCanonical) {
					t.Fatalf("published canonical changed: %+v err=%v", shown, err)
				}
			}
			output, err = runIterationProcess(t, board, "same", "")
			want := "registered"
			if phase == "after-link" {
				want = "unchanged"
			}
			if err != nil || strings.TrimSpace(string(output)) != want {
				t.Fatalf("retry=%s err=%v want=%s", output, err, want)
			}
		})
	}
}

func TestContextIterationConcurrentProcesses(t *testing.T) {
	t.Parallel()
	for _, distinct := range []bool{false, true} {
		t.Run(fmt.Sprintf("distinct-content-%v", distinct), func(t *testing.T) {
			board := filepath.Join(t.TempDir(), "tasks")
			if err := Init(board); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				output string
				err    error
			}
			results := make(chan outcome, 2)
			for i := 0; i < 2; i++ {
				variant := "same"
				if distinct && i == 1 {
					variant = "different"
				}
				go func(variant string) {
					output, err := runIterationProcess(t, board, variant, "")
					results <- outcome{strings.TrimSpace(string(output)), err}
				}(variant)
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
					t.Fatalf("unexpected iteration subprocess result: %+v", result)
				}
			}
			if registered != 1 || (!distinct && unchanged != 1) || (distinct && conflicts != 1) {
				t.Fatalf("registered=%d unchanged=%d conflicts=%d", registered, unchanged, conflicts)
			}
			if _, err := ShowContext(board, "iteration", "ITERATION-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1); err != nil {
				t.Fatal(err)
			}
			_, intent := maintenanceIntentRaw(t)
			wantIntent, _ := intent.Canonical()
			shownIntent, err := ShowContext(board, "intent", maintenanceIntentID, 1)
			if err != nil || !bytes.Equal(shownIntent.Canonical, wantIntent) {
				t.Fatalf("concurrent intent canonical changed: %+v err=%v", shownIntent, err)
			}
			sameRaw := maintenanceIterationRaw(t, intent, "ITERATION-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "idle", nil, nil)
			differentRaw := []byte(strings.Replace(string(sameRaw), `"reason":"observed"`, `"reason":"different"`, 1))
			if bytes.Equal(sameRaw, differentRaw) {
				t.Fatal("different contender mutation missed its target")
			}
			validCanonical := make([][]byte, 0, 2)
			for _, raw := range [][]byte{sameRaw, differentRaw} {
				doc, parseErr := intentdoc.Parse(raw)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				canonical, canonicalErr := doc.Canonical()
				if canonicalErr != nil {
					t.Fatal(canonicalErr)
				}
				validCanonical = append(validCanonical, canonical)
			}
			shown, err := ShowContext(board, "iteration", "ITERATION-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1)
			matchesContender := bytes.Equal(shown.Canonical, validCanonical[0]) || (distinct && bytes.Equal(shown.Canonical, validCanonical[1]))
			if err != nil || !matchesContender {
				t.Fatalf("concurrent iteration canonical is not a contender: %+v err=%v", shown, err)
			}
		})
	}
}
