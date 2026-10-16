//go:build unix

package declaration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

type fixtureWorld struct {
	writer                  *FixtureWriter
	record                  Record
	link                    Link
	sandbox, common, ledger string
}

func writerWorld(t *testing.T) fixtureWorld {
	t.Helper()
	sandbox, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(sandbox, 0o700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CE_TASK_ACTOR", "example-agent")
	t.Setenv("GIT_WORK_HOST", "example-device")
	root := filepath.Join(sandbox, "repo")
	if e := os.Mkdir(root, 0o700); e != nil {
		t.Fatal(e)
	}
	c := exec.Command("git", "init", "--quiet", root)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("synthetic git init: %v %s", e, b)
	}
	common := filepath.Join(root, ".git")
	if e := os.Mkdir(filepath.Join(root, ".ce"), 0o700); e != nil {
		t.Fatal(e)
	}
	config := "schema-version: 2\nrepository-id: example\nworktree-roots:\n  example-device: " + sandbox + "\nintegration-provider: gz-git\nintegration-network-policy: no-fetch\n"
	if e := os.WriteFile(filepath.Join(root, taskruntime.ConfigFile), []byte(config), 0o600); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("testdata/bound.json")
	if e != nil {
		t.Fatal(e)
	}
	r, e := Decode(b)
	if e != nil {
		t.Fatal(e)
	}
	r.Repository.CommonDir = common
	r.Execution.Snapshot.Worktree = root
	started, _ := time.Parse(time.RFC3339Nano, r.Execution.Snapshot.StartedAt)
	x := r.Execution.Snapshot
	state := filepath.Join(common, taskruntime.RuntimeStateDir, "task-runtime", "v1")
	if e := os.MkdirAll(state, 0o755); e != nil {
		t.Fatal(e)
	}
	b, e = json.Marshal([]taskruntime.Execution{{Task: x.Task, Type: x.Type, Purpose: taskruntime.Purpose(x.Purpose), Source: x.Source, Owner: x.Owner, Branch: x.Branch, Worktree: x.Worktree, StartedAt: started, UpdatedAt: started}})
	if e != nil {
		t.Fatal(e)
	}
	ledger := filepath.Join(state, "executions.json")
	if e := os.WriteFile(ledger, b, 0o644); e != nil {
		t.Fatal(e)
	}
	w, e := NewFixtureWriter(sandbox, taskruntime.NewService(root, taskruntime.NewExecutor()))
	if e != nil {
		t.Fatal(e)
	}
	l := Link{Schema: LinkSchemaV1, EventID: "example-event", OccurredAt: r.OccurredAt, Repository: r.Repository, Runtime: r.Runtime, DeclarationID: r.ID}
	return fixtureWorld{w, r, l, sandbox, common, ledger}
}

func TestFixtureWriterReplayConflictAndHistory(t *testing.T) {
	w := writerWorld(t)
	before, _ := os.ReadFile(w.ledger)
	deliveries := 0
	consumer := func(Link) error { deliveries++; return nil }
	first, e := w.writer.Write(context.Background(), w.record, w.link, consumer)
	if e != nil || !first.Durable || first.Disposition != "created" || first.Audit != "confirmed" {
		t.Fatalf("first: %+v %v", first, e)
	}
	second, e := w.writer.Write(context.Background(), w.record, w.link, consumer)
	if e != nil || second.Disposition != "duplicate" || !second.Durable {
		t.Fatalf("replay: %+v %v", second, e)
	}
	changed := w.record
	changed.OccurredAt = "2026-01-01T00:00:02Z"
	if _, e = w.writer.Write(context.Background(), changed, w.link, consumer); !errors.Is(e, ErrFixtureConflict) {
		t.Fatal("declaration overwritten", e)
	}
	link := w.link
	link.OccurredAt = "2026-01-01T00:00:03Z"
	if _, e = w.writer.Write(context.Background(), w.record, link, consumer); !errors.Is(e, ErrFixtureConflict) {
		t.Fatal("event overwritten", e)
	}
	if deliveries != 2 {
		t.Fatal("conflict delivered")
	}
	current := w.record.Execution.Snapshot
	current.StartedAt = "2026-01-01T01:00:00Z"
	view := first.Stored.View(w.record.Repository, &current, taskruntime.StatusActive)
	if view.Provenance != "bound-at-write" || view.Current != "different-generation" {
		t.Fatalf("history lost: %+v", view)
	}
	if (FixtureStored{}).View(w.record.Repository, &current, taskruntime.StatusActive).Provenance != "unknown" {
		t.Fatal("forged provenance")
	}
	if Assess(w.record, w.record.Repository, &w.record.Execution.Snapshot, taskruntime.StatusActive).Provenance != "unknown" {
		t.Fatal("payload awarded provenance")
	}
	after, _ := os.ReadFile(w.ledger)
	if string(after) != string(before) {
		t.Fatal("execution changed")
	}
	b, _ := json.Marshal(view)
	if strings.Contains(string(b), w.common) || strings.Contains(string(b), "example-agent") {
		t.Fatal("private data exported")
	}
}

func TestFixtureWriterFaultsAndAuditUnknown(t *testing.T) {
	for _, stage := range []string{"declaration-create", "declaration-write", "declaration-file-sync", "declaration-rename", "declaration-directory-sync", "link-create", "link-directory-sync", "consumer"} {
		t.Run(stage, func(t *testing.T) {
			w := writerWorld(t)
			before, _ := os.ReadFile(w.ledger)
			w.writer.fault = func(s string) error {
				if s == stage {
					return errors.New("synthetic fault")
				}
				return nil
			}
			result, e := w.writer.Write(context.Background(), w.record, w.link, func(Link) error {
				if stage == "consumer" {
					return errors.New("synthetic consumer fault")
				}
				return nil
			})
			if e == nil || result.Audit != "unconfirmed" {
				t.Fatalf("fault hidden: %+v %v", result, e)
			}
			wantDurable := strings.HasPrefix(stage, "link-") || stage == "consumer"
			if result.Durable != wantDurable {
				t.Fatalf("false durability: %+v", result)
			}
			if stage == "declaration-directory-sync" && !errors.Is(e, ErrFixtureDurability) {
				t.Fatal("durability unknown hidden")
			}
			w.writer.fault = nil
			result, e = w.writer.Write(context.Background(), w.record, w.link, func(Link) error { return nil })
			if e != nil || !result.Durable || result.Audit != "confirmed" {
				t.Fatalf("retry: %+v %v", result, e)
			}
			after, _ := os.ReadFile(w.ledger)
			if string(before) != string(after) {
				t.Fatal("failed persistence changed execution")
			}
			entries, e := os.ReadDir(filepath.Join(w.common, "declaration-fixture-v1"))
			if e != nil {
				t.Fatal(e)
			}
			if len(entries) != 2 {
				t.Fatalf("temporary debris or duplicate: %d", len(entries))
			}
		})
	}
}

func TestFixtureWriterPrivacyAndBoundary(t *testing.T) {
	for _, scenario := range []string{"topic", "broad-sandbox", "symlink-sandbox", "broad-collection", "symlink-collection", "broad-record", "symlink-record"} {
		t.Run(scenario, func(t *testing.T) {
			w := writerWorld(t)
			collection := filepath.Join(w.common, "declaration-fixture-v1")
			if scenario == "topic" {
				secret := "synthetic-secret-never-store"
				w.record.Subject.Topic = &secret
				w.record.Privacy.State = "redacted"
			}
			if scenario == "broad-sandbox" {
				if e := os.Chmod(w.sandbox, 0o755); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "symlink-sandbox" {
				alias := filepath.Join(t.TempDir(), "alias")
				if e := os.Symlink(w.sandbox, alias); e != nil {
					t.Fatal(e)
				}
				w.writer.sandbox = alias
			}
			if scenario == "broad-collection" {
				if e := os.Mkdir(collection, 0o755); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "symlink-collection" {
				if e := os.Symlink(t.TempDir(), collection); e != nil {
					t.Fatal(e)
				}
			}
			if strings.HasSuffix(scenario, "record") {
				if _, e := w.writer.Write(context.Background(), w.record, w.link, nil); e != nil {
					t.Fatal(e)
				}
				p := filepath.Join(collection, fixtureName("declaration", w.record.ID))
				if scenario == "broad-record" {
					if e := os.Chmod(p, 0o644); e != nil {
						t.Fatal(e)
					}
				} else {
					if e := os.Remove(p); e != nil {
						t.Fatal(e)
					}
					if e := os.Symlink(w.ledger, p); e != nil {
						t.Fatal(e)
					}
				}
			}
			if _, e := w.writer.Write(context.Background(), w.record, w.link, nil); e == nil {
				t.Fatal("unsafe input persisted")
			}
			if scenario == "topic" {
				if _, e := os.Stat(collection); !os.IsNotExist(e) {
					t.Fatal("topic reached storage")
				}
			}
		})
	}
}

func TestFixtureWriterConcurrentSessions(t *testing.T) {
	w := writerWorld(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, e := w.writer.Write(context.Background(), w.record, w.link, nil)
			if e == nil && r.Durable {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if success < 1 {
		t.Fatal("no serialized write succeeded")
	}
	r, e := w.writer.Write(context.Background(), w.record, w.link, nil)
	if e != nil || r.Disposition != "duplicate" {
		t.Fatal("concurrent retry conflict", e)
	}
}
