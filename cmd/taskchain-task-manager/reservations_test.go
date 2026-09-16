package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReserveIDsCLIIdempotentAndUnion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if code := run([]string{"init", "--dir", dir, "--json"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal(code)
	}
	call := func(ids ...string) map[string]any {
		args := []string{"reserve-ids", "--dir", dir}
		for _, id := range ids {
			args = append(args, "--id", id)
		}
		args = append(args, "--json")
		var out, diag bytes.Buffer
		if code := runReservations(args, &out, &diag); code != 0 {
			t.Fatalf("code=%d stderr=%s", code, diag.String())
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := call("TASK-2", "TASK-1"); got["reservedCount"] != float64(2) || got["maxId"] != "TASK-2" {
		t.Fatalf("result=%v", got)
	}
	if got := call("TASK-1"); got["reservedCount"] != float64(2) {
		t.Fatalf("repeat=%v", got)
	}
}

func TestReserveIDsCLIUsageAndAdopt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "legacy")
	if err := os.MkdirAll(filepath.Join(dir, "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := runReservations([]string{"reserve-ids", "--dir", dir, "--id", "TASK-1", "--json"}, &out, &diag); code != 1 || out.Len() != 0 {
		t.Fatalf("missing ledger code=%d out=%s", code, out.String())
	}
	out.Reset()
	diag.Reset()
	if code := runReservations([]string{"reserve-ids", "--dir", dir, "--adopt", "--json"}, &out, &diag); code != 0 {
		t.Fatalf("adopt code=%d stderr=%s", code, diag.String())
	}
	for _, args := range [][]string{{"reserve-ids", "--dir", dir, "--json"}, {"reserve-ids", "--dir", dir, "--id", "TASK-3"}} {
		out.Reset()
		diag.Reset()
		if code := runReservations(args, &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatalf("usage=%v code=%d out=%q", args, code, out.String())
		}
	}
	out.Reset()
	diag.Reset()
	if code := runReservations([]string{"reserve-ids", "--dir", dir, "--id", "task-1", "--json"}, &out, &diag); code != 1 || out.Len() != 0 {
		t.Fatalf("invalid id code=%d out=%q", code, out.String())
	}
}

func TestReserveIDsOutputFailureIsRetryable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if code := run([]string{"init", "--dir", dir, "--json"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal(code)
	}
	if code := runReservations([]string{"reserve-ids", "--dir", dir, "--id", "TASK-8", "--json"}, failClaimOutput{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("code=%d", code)
	}
	var out, diag bytes.Buffer
	if code := runReservations([]string{"reserve-ids", "--dir", dir, "--id", "TASK-8", "--json"}, &out, &diag); code != 0 || !strings.Contains(out.String(), "TASK-8") {
		t.Fatalf("retry code=%d out=%s", code, out.String())
	}
}
