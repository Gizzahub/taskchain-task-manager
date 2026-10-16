package declaration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func restartWorld(t *testing.T) (fixtureWorld, *RestartFixtureWriter) {
	t.Helper()
	world := writerWorld(t)
	restarted, err := NewRestartFixtureWriter(world.sandbox, world.writer.runtime)
	if err != nil {
		t.Fatal(err)
	}
	return world, restarted
}

func TestRestartFixtureRoundTrip(t *testing.T) {
	world, writer := restartWorld(t)
	before, err := os.ReadFile(world.ledger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Read(world.record.Repository, world.record.ID); err == nil {
		t.Fatal("missing collection trusted")
	}
	if _, err := os.Stat(filepath.Join(world.common, writer.writer.collectionName())); !os.IsNotExist(err) {
		t.Fatal("reader created collection")
	}
	result, err := writer.Write(context.Background(), world.record, world.link, nil)
	if err != nil || !result.Durable {
		t.Fatalf("write: %+v %v", result, err)
	}
	reader, err := NewRestartFixtureWriter(world.sandbox, world.writer.runtime)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reader.Read(world.record.Repository, world.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := stored.View(world.record.Repository, &world.record.Execution.Snapshot, taskruntime.StatusActive)
	if view.Provenance != "bound-at-write" || view.Current != "matching" {
		t.Fatalf("view: %+v", view)
	}
	b, err := os.ReadFile(filepath.Join(world.common, writer.writer.collectionName(), fixtureName("declaration", world.record.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(b); err == nil {
		t.Fatal("envelope accepted as declaration v1")
	}
	decoded, err := decodeRestartFixture(b)
	if err != nil {
		t.Fatal(err)
	}
	if Assess(decoded, decoded.Repository, &decoded.Execution.Snapshot, taskruntime.StatusActive).Provenance != "unknown" {
		t.Fatal("decode granted trust")
	}
	alias := "untrusted-alias"
	repo := world.record.Repository
	repo.CatalogAlias = &alias
	if _, err := reader.Read(repo, world.record.ID); err == nil {
		t.Fatal("repository identity bypass")
	}
	changed := world.record
	changed.OccurredAt = "2026-01-02T00:00:00Z"
	if _, err := writer.Write(context.Background(), changed, world.link, nil); !errors.Is(err, ErrFixtureConflict) {
		t.Fatalf("conflict: %v", err)
	}
	second, err := writer.Write(context.Background(), world.record, world.link, nil)
	if err != nil || second.Disposition != "duplicate" {
		t.Fatalf("replay: %+v %v", second, err)
	}
	after, err := os.ReadFile(world.ledger)
	if err != nil || string(before) != string(after) {
		t.Fatal("execution changed")
	}
	exported, _ := json.Marshal(view)
	if strings.Contains(string(exported), world.common) || strings.Contains(string(exported), world.record.Execution.Snapshot.Owner.Actor) {
		t.Fatal("view leaked protected identity")
	}
}

func TestRestartFixtureRejectsUntrustedStorage(t *testing.T) {
	for _, kind := range []string{"legacy", "schema", "writer", "id", "topic", "mode", "symlink", "ancestor", "outside"} {
		t.Run(kind, func(t *testing.T) {
			world, writer := restartWorld(t)
			if _, err := writer.Write(context.Background(), world.record, world.link, nil); err != nil {
				t.Fatal(err)
			}
			collection := filepath.Join(world.common, writer.writer.collectionName())
			file := filepath.Join(collection, fixtureName("declaration", world.record.ID))
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "legacy":
				b, err = json.Marshal(world.record)
			case "schema":
				b = []byte(strings.Replace(string(b), restartFixtureSchema, "unsupported/v9", 1))
			case "writer":
				b = []byte(strings.Replace(string(b), restartFixtureWriter, "forged/v9", 1))
			case "id":
				var e restartFixtureEnvelope
				if err := json.Unmarshal(b, &e); err != nil {
					t.Fatal(err)
				}
				e.Record.ID = "different-id"
				b, err = json.Marshal(e)
			case "topic":
				var e restartFixtureEnvelope
				if err := json.Unmarshal(b, &e); err != nil {
					t.Fatal(err)
				}
				topic := "example"
				e.Record.Subject.Topic = &topic
				e.Record.Privacy.State = "redacted"
				e.Record.Privacy.Reason = nil
				b, err = json.Marshal(e)
			case "mode":
				if err := os.Chmod(file, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(world.sandbox, "copied.json")
				if err := os.WriteFile(target, b, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, file); err != nil {
					t.Fatal(err)
				}
			case "ancestor":
				if err := os.Chmod(world.common, 0o777); err != nil {
					t.Fatal(err)
				}
			case "outside":
				world.record.Repository.CommonDir = t.TempDir()
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "legacy" || kind == "schema" || kind == "writer" || kind == "id" || kind == "topic" {
				if err := os.WriteFile(file, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := writer.Read(world.record.Repository, world.record.ID)
			if err == nil || stored.trusted {
				t.Fatalf("%s trusted: %+v %v", kind, stored, err)
			}
		})
	}
	// The old fixture namespace is never an implicit migration source.
	world, writer := restartWorld(t)
	if _, err := world.writer.Write(context.Background(), world.record, world.link, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Read(world.record.Repository, world.record.ID); err == nil {
		t.Fatal("legacy namespace auto-promoted")
	}
}

func TestRestartFixtureHistoricalAfterTerminal(t *testing.T) {
	world, writer := restartWorld(t)
	if _, err := writer.Write(context.Background(), world.record, world.link, nil); err != nil {
		t.Fatal(err)
	}
	// Historical reader needs no execution bytes, and never recreates the ledger.
	if err := os.Remove(world.ledger); err != nil {
		t.Fatal(err)
	}
	stored, err := writer.Read(world.record.Repository, world.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{taskruntime.StatusDone, taskruntime.StatusAborted, "unavailable"} {
		view := stored.View(world.record.Repository, nil, status)
		if view.Provenance != "bound-at-write" || view.Current == "matching" {
			t.Fatalf("historical/current conflated: %+v", view)
		}
	}
	if _, err := os.Stat(world.ledger); !os.IsNotExist(err) {
		t.Fatal("reader created ledger")
	}
	if _, err := writer.Write(context.Background(), world.record, world.link, nil); err == nil {
		t.Fatal("write without active generation")
	}
}

func TestRestartFixtureFaultReplay(t *testing.T) {
	for _, stage := range []string{"declaration-write", "declaration-file-sync", "declaration-rename", "declaration-directory-sync", "link-directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			world, writer := restartWorld(t)
			writer.writer.fault = func(s string) error {
				if s == stage {
					return errors.New("synthetic fault")
				}
				return nil
			}
			if _, err := writer.Write(context.Background(), world.record, world.link, nil); err == nil {
				t.Fatal("fault hidden")
			}
			writer.writer.fault = nil
			if _, err := writer.Write(context.Background(), world.record, world.link, func(Link) error { return errors.New("synthetic consumer") }); err == nil {
				t.Fatal("consumer failure hidden")
			}
			result, err := writer.Write(context.Background(), world.record, world.link, nil)
			if err != nil || !result.Durable || result.Disposition != "duplicate" {
				t.Fatalf("retry: %+v %v", result, err)
			}
			for _, readStage := range []string{"restart-read-file-sync", "restart-read-directory-sync"} {
				writer.writer.fault = func(s string) error {
					if s == readStage {
						return errors.New("synthetic fault")
					}
					return nil
				}
				stored, err := writer.Read(world.record.Repository, world.record.ID)
				if err == nil || stored.trusted {
					t.Fatal("read sync failure granted trust")
				}
			}
			writer.writer.fault = nil
			if _, err := writer.Read(world.record.Repository, world.record.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRestartFixtureSubprocessHelper(t *testing.T) {
	if os.Getenv("GO_TEST_RESTART_FIXTURE_HELPER") != "1" {
		return
	}
	var input struct {
		Sandbox string
		Record  Record
		Link    Link
		Crash   bool
	}
	if json.Unmarshal([]byte(os.Getenv("GO_TEST_RESTART_FIXTURE_INPUT")), &input) != nil {
		os.Exit(21)
	}
	runtime := taskruntime.NewService(filepath.Dir(input.Record.Repository.CommonDir), taskruntime.NewExecutor())
	writer, err := NewRestartFixtureWriter(input.Sandbox, runtime)
	if err != nil {
		os.Exit(22)
	}
	if input.Crash {
		writer.writer.fault = func(stage string) error {
			if stage == "declaration-directory-sync" {
				os.Exit(23)
			}
			return nil
		}
		_, _ = writer.Write(context.Background(), input.Record, input.Link, nil)
		os.Exit(24)
	}
	stored, err := writer.Read(input.Record.Repository, input.Record.ID)
	if err != nil || stored.View(input.Record.Repository, nil, "unavailable").Provenance != "bound-at-write" {
		os.Exit(25)
	}
	os.Exit(0)
}

func TestRestartFixtureSubprocessRestart(t *testing.T) {
	world, writer := restartWorld(t)
	invoke := func(crash bool) error {
		input := struct {
			Sandbox string
			Record  Record
			Link    Link
			Crash   bool
		}{world.sandbox, world.record, world.link, crash}
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestRestartFixtureSubprocessHelper$")
		cmd.Env = append(os.Environ(), "GO_TEST_RESTART_FIXTURE_HELPER=1", "GO_TEST_RESTART_FIXTURE_INPUT="+string(data))
		return cmd.Run()
	}
	err := invoke(true)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("expected actual post-rename crash, got %v", err)
	}
	// Retry reclaims only the known-dead synthetic lock and completes durability/link.
	result, err := writer.Write(context.Background(), world.record, world.link, nil)
	if err != nil || !result.Durable || result.Disposition != "duplicate" {
		t.Fatalf("crash retry: %+v %v", result, err)
	}
	if err := invoke(false); err != nil {
		t.Fatalf("independent process restore: %v", err)
	}
}
