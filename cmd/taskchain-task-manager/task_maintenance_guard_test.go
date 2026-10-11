package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardruntime"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("sink closed") }

// An applied exchange followed by a failed result write must exit 3 with the
// "do not rerun" guidance, in both output modes.
func TestMaintenancePostExchangeWriteFailureExitsThree(t *testing.T) {
	for _, args := range [][]string{{"--apply", "--json"}, {"--apply"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			plan, _ := writeMaintenanceFixture(t, "")
			err := taskMaintenance(context.Background(), append(args, plan), failingWriter{})
			var exit *runExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 3 {
				t.Fatalf("err=%v want runExitError code 3", err)
			}
			if !strings.HasPrefix(exit.Error(), "applied, do not rerun:") || !strings.Contains(exit.Error(), "sink closed") {
				t.Fatalf("reason=%q", exit.Error())
			}
			if _, statErr := os.Stat("tasks/.ce-maintenance-receipt.json"); statErr != nil {
				t.Fatalf("exchange did not apply: %v", statErr)
			}
		})
	}
}

// A board file edited after plan validation but before the exchange must be
// refused with the board left as the editor wrote it.
func TestMaintenanceRefusesLiveBeforeDrift(t *testing.T) {
	t.Chdir(t.TempDir())
	plan, _ := writeMaintenanceFixture(t, "")
	edited := "---\nid: TASK-1\ntitle: Edited\n---\n"
	t.Cleanup(func() { boardruntime.TestingBeforeExchange = nil })
	boardruntime.TestingBeforeExchange = func() {
		if err := os.WriteFile("tasks/todo/001-card.md", []byte(edited), 0o644); err != nil {
			t.Error(err)
		}
	}
	var out, diag bytes.Buffer
	code := runTask([]string{"maintenance", "--apply", plan}, &out, &diag)
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "before file tasks/todo/001-card.md changed before exchange") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
	if mustRead(t, "tasks/todo/001-card.md") != edited {
		t.Fatal("refused apply replaced the edited card")
	}
	if _, err := os.Stat("tasks/.ce-maintenance-receipt.json"); !os.IsNotExist(err) {
		t.Fatalf("receipt present after refusal: %v", err)
	}
	assertNoMutationLock(t)
}
