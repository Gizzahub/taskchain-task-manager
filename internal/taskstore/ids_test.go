package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDeletedIDsAndExplicitHoles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"TASK-9", "TASK-2"} {
		e, err := Create(dir, CreateRequest{ID: id, Title: "synthetic"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, e.Path)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Create(dir, CreateRequest{ID: "TASK-9", Title: "reuse"}); err == nil {
		t.Fatal("deleted ID reused")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("rejected reuse modified board")
	}
	e, err := Create(dir, CreateRequest{Title: "next"})
	if err != nil || e.Card.ID != "TASK-10" {
		t.Fatalf("next=%v err=%v", e, err)
	}
}

func TestInterruptedCreateBurnsReservation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("synthetic interruption")
	_, err := createWithStep(dir, CreateRequest{Title: "interrupted"}, func(string) error { return stop })
	if !errors.Is(err, stop) || !strings.Contains(err.Error(), "TASK-1 is reserved") {
		t.Fatalf("error=%v", err)
	}
	entries, err := List(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("card published: %v %v", entries, err)
	}
	e, err := Create(dir, CreateRequest{Title: "next"})
	if err != nil || e.Card.ID != "TASK-2" {
		t.Fatalf("reservation reused: %v %v", e, err)
	}
}

func TestAdoptionMustBeExplicitAndNeverResets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := os.MkdirAll(filepath.Join(dir, "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-3\n---\n# Old\n")
	if err := os.WriteFile(filepath.Join(dir, "todo/old.md"), raw, 0o640); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if err := Init(dir); err == nil {
		t.Fatal("legacy init silently adopted")
	}
	if _, err := Create(dir, CreateRequest{Title: "unsafe"}); err == nil {
		t.Fatal("legacy create silently adopted")
	}
	if _, err := ReserveIDs(dir, []string{"TASK-8"}, false); err == nil {
		t.Fatal("missing ledger accepted")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("failed adoption changed board")
	}
	if _, err := List(dir); err != nil {
		t.Fatalf("legacy read unavailable: %v", err)
	}
	result, err := ReserveIDs(dir, []string{"TASK-8"}, true)
	if err != nil || result.ReservedCount != 2 || result.MaxID != "TASK-8" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	after := boardBytes(t, dir)
	if _, err := ReserveIDs(dir, []string{"TASK-8"}, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, boardBytes(t, dir)) {
		t.Fatal("repeat adoption changed board")
	}
	e, err := Create(dir, CreateRequest{Title: "next"})
	if err != nil || e.Card.ID != "TASK-9" {
		t.Fatalf("adopted history ignored: %v %v", e, err)
	}
}

func TestReservationOverflowAndHole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveIDs(dir, []string{"TASK-18446744073709551615"}, false); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Create(dir, CreateRequest{Title: "overflow"}); err == nil {
		t.Fatal("overflow accepted")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("overflow changed board")
	}
	if _, err := Create(dir, CreateRequest{ID: "TASK-1", Title: "hole"}); err != nil {
		t.Fatal(err)
	}
}

func TestInitialLedgerPublishNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveIDs(dir, []string{"TASK-7"}, false); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := publishIDs(r, idLedger{SchemaVersion: 1, Reserved: []string{}}, true); err == nil {
		t.Fatal("initial publication overwrote existing ledger")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("initial publication changed board")
	}
}
