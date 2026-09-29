package cardid

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLedger(t *testing.T, base, content string) {
	t.Helper()
	dir := filepath.Join(base, reservationDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, reservationFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readLedger(t *testing.T, base string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(base, reservationDirName, reservationFileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReservationLedgerNextReservesMaxOfLedgerAndFloor(t *testing.T) {
	base := t.TempDir()
	writeLedger(t, base, "{\"TASK\": 5}\n")
	ledger := NewReservationLedger(base)

	n, err := ledger.Next("TASK", 1)
	if err != nil || n != 6 {
		t.Fatalf("Next ledger floor: n=%d err=%v", n, err)
	}
	// The saved spelling is the durable contract, not the compact input.
	if got := readLedger(t, base); got != "{\n  \"TASK\": 6\n}\n" {
		t.Fatalf("ledger bytes: %q", got)
	}

	if n, err = ledger.Next("TASK", 10); err != nil || n != 11 {
		t.Fatalf("Next tree floor above ledger: n=%d err=%v", n, err)
	}
	// Prefixes reserve independently; one prefix's ceiling is another's floor.
	if n, err = ledger.Next("PLAN", 0); err != nil || n != 1 {
		t.Fatalf("Next other prefix: n=%d err=%v", n, err)
	}
	if got := readLedger(t, base); got != "{\n  \"PLAN\": 1,\n  \"TASK\": 11\n}\n" {
		t.Fatalf("ledger after two prefixes: %q", got)
	}
	if _, err := os.Stat(filepath.Join(base, reservationDirName, reservationLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock survived reservation: %v", err)
	}
}

func TestReservationLedgerRefusesFullSpaceWithoutWriting(t *testing.T) {
	base := t.TempDir()
	writeLedger(t, base, "{\n  \"TASK\": 999\n}\n")

	if _, err := NewReservationLedger(base).Next("TASK", 0); !errors.Is(err, ErrNumberSpaceFull) {
		t.Fatalf("refusal: err=%v", err)
	}
	// The refusal must not burn the number: reserving 1000 and failing the
	// filename pattern would leave the next try at 1001.
	if got := readLedger(t, base); got != "{\n  \"TASK\": 999\n}\n" {
		t.Fatalf("ledger changed on refusal: %q", got)
	}
}

func TestReservationLedgerMissingLedgerIsAnEmptyBoard(t *testing.T) {
	base := t.TempDir()
	n, err := NewReservationLedger(base).Next("TASK", 0)
	if err != nil || n != 1 {
		t.Fatalf("first reservation: n=%d err=%v", n, err)
	}
	if err := os.MkdirAll(filepath.Join(base, reservationDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, reservationDirName, reservationFileName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReservationLedger(base).Next("TASK", 0); err == nil || strings.Contains(err.Error(), "decode") == false {
		t.Fatalf("corrupt ledger: err=%v", err)
	}
}
