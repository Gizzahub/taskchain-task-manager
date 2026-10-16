//go:build unix

package declaration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

// The environment seam exists only in the test binary, never in the product.
func TestFixtureWriterCrashHelper(t *testing.T) {
	sandbox := os.Getenv("GO_TEST_DECLARATION_SANDBOX")
	if sandbox == "" {
		return
	}
	r, e := Decode([]byte(os.Getenv("GO_TEST_DECLARATION_RECORD")))
	if e != nil {
		t.Fatal(e)
	}
	l, e := DecodeLink([]byte(os.Getenv("GO_TEST_DECLARATION_LINK")))
	if e != nil {
		t.Fatal(e)
	}
	w, e := NewFixtureWriter(sandbox, taskruntime.NewService(r.Execution.Snapshot.Worktree, taskruntime.NewExecutor()))
	if e != nil {
		t.Fatal(e)
	}
	w.fault = func(stage string) error {
		if stage == "declaration-directory-sync" {
			os.Exit(23)
		}
		return nil
	}
	if _, e := w.Write(context.Background(), r, l, nil); e != nil {
		t.Fatal(e)
	}
	t.Fatal("crash boundary was not reached")
}

func TestFixtureWriterCrashAndRetry(t *testing.T) {
	w := writerWorld(t)
	b, _ := json.Marshal(w.record)
	lb, _ := json.Marshal(w.link)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFixtureWriterCrashHelper$")
	cmd.Env = append(os.Environ(), "GO_TEST_DECLARATION_SANDBOX="+w.sandbox, "GO_TEST_DECLARATION_RECORD="+string(b), "GO_TEST_DECLARATION_LINK="+string(lb))
	output, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("not the expected abrupt exit: %v %s", e, output)
	}
	if _, e := os.Stat(filepath.Join(w.common, "declaration-fixture-v1", fixtureName("declaration", w.record.ID))); e != nil {
		t.Fatal("published record lost", e)
	}
	// Existing mutation-lock recovery uses the same-host dead PID, never age.
	result, e := w.writer.Write(context.Background(), w.record, w.link, nil)
	if e != nil || !result.Durable || result.Disposition != "duplicate" || result.Audit != "unconfirmed" {
		t.Fatalf("recovery: %+v %v", result, e)
	}
}

func TestFixtureWriterPinsCollectionDuringReplacement(t *testing.T) {
	w := writerWorld(t)
	collection := filepath.Join(w.common, "declaration-fixture-v1")
	retained := collection + "-retained"
	out, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	w.writer.fault = func(stage string) error {
		if stage == "declaration-write" {
			if e := os.Rename(collection, retained); e != nil {
				return e
			}
			if e := os.Symlink(out, collection); e != nil {
				return e
			}
		}
		return nil
	}
	result, e := w.writer.Write(context.Background(), w.record, w.link, nil)
	if !errors.Is(e, ErrFixtureDurability) || result.Durable {
		t.Fatalf("replacement not reported as unknown: %+v %v", result, e)
	}
	entries, e := os.ReadDir(out)
	if e != nil || len(entries) != 0 {
		t.Fatal("replacement redirected storage", e)
	}
	if _, e := os.Stat(filepath.Join(retained, fixtureName("declaration", w.record.ID))); e != nil {
		t.Fatal("pinned write lost", e)
	}
	w.writer.fault = nil
	if _, e := w.writer.Write(context.Background(), w.record, w.link, nil); e == nil {
		t.Fatal("later symlink access accepted")
	}
}

func TestFixtureStoredReportsCurrentReasonSeparately(t *testing.T) {
	w := writerWorld(t)
	result, e := w.writer.Write(context.Background(), w.record, w.link, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct{ status, current, reason string }{
		{taskruntime.StatusActive, "matching", "current-generation-matches"},
		{taskruntime.StatusDone, "terminal", "current-execution-terminal"},
		{taskruntime.StatusActive, "different-generation", "current-generation-different"},
		{"unknown", "unavailable", "current-execution-state-unavailable"},
	} {
		current := w.record.Execution.Snapshot
		if c.current == "different-generation" {
			current.StartedAt = "2026-01-01T01:00:00Z"
		}
		view := result.Stored.View(w.record.Repository, &current, c.status)
		if view.Provenance != "bound-at-write" || view.Current != c.current || view.Reason != c.reason {
			t.Fatalf("contradictory trusted view: %+v", view)
		}
	}
}
