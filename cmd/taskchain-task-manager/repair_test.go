package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repairDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func runRepairCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := run(args, &out, &diagnostics)
	return code, out.String(), diagnostics.String()
}

func repairFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if code, _, diagnostics := runRepairCLI(t, "init", "--dir", dir, "--json"); code != 0 {
		t.Fatalf("init code=%d stderr=%s", code, diagnostics)
	}
	if code, _, diagnostics := runRepairCLI(t, "create", "--dir", dir, "--title", "repair", "--json"); code != 0 {
		t.Fatalf("create code=%d stderr=%s", code, diagnostics)
	}
	path := filepath.Join(dir, "todo", "TASK-1.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return dir, path, original
}

func replaceFlag(args []string, name, value string) []string {
	out := append([]string(nil), args...)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == name {
			out[i+1] = value
			return out
		}
	}
	return out
}

func removeFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func wrongStatusBytes(t *testing.T, original []byte) []byte {
	t.Helper()
	mutated := bytes.Replace(original, []byte("| **Status** | [ ] Pending |"), []byte("| **Status** | [x] Done |"), 1)
	if bytes.Equal(mutated, original) {
		t.Fatal("fixture has no canonical Status cell")
	}
	return mutated
}

func canonicalStatusBytes(t *testing.T, original []byte) []byte {
	t.Helper()
	return append(append([]byte(nil), original...), []byte("\n| **Status** | [ ] Pending |\n")...)
}

func TestRepairStatusCLIHelp(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := runRepairStatus([]string{"repair-status", "--help"}, &out, &diagnostics); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, diagnostics.String())
	}
	if !strings.Contains(out.String(), "Usage: repair-status") || diagnostics.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), diagnostics.String())
	}
}

func TestRepairStatusCLIUsageDoesNotWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	base := []string{"repair-status", "--dir", dir, "--id", "TASK-1", "--path", "todo/TASK-1.md", "--owner", "tester", "--request-id", strings.Repeat("a", 32), "--expected-sha256", strings.Repeat("b", 64), "--json"}
	for _, args := range [][]string{
		{"repair-status", "--dir", dir, "--json"},
		append(append([]string(nil), base...), "--adopt", "--resume"),
	} {
		var out, diagnostics bytes.Buffer
		if code := runRepairStatus(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, out.String(), diagnostics.String())
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("usage created board: %v", err)
	}
}

func TestRepairStatusCLIDomainErrorHasEmptyStdout(t *testing.T) {
	args := []string{
		"repair-status", "--dir", filepath.Join(t.TempDir(), "absent"),
		"--id", "TASK-1", "--path", "todo/TASK-1.md", "--owner", "tester",
		"--request-id", strings.Repeat("a", 32), "--expected-sha256", strings.Repeat("b", 64), "--json",
	}
	var out, diagnostics bytes.Buffer
	if code := runRepairStatus(args, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
	}
}

func TestRepairStatusCLIAdoptReplayConflictAndHashGuard(t *testing.T) {
	dir, path, original := repairFixture(t)
	baseline := canonicalStatusBytes(t, original)
	mutated := wrongStatusBytes(t, baseline)
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := repairDigest(mutated)
	request := strings.Repeat("c", 32)
	args := []string{"repair-status", "--dir", dir, "--id", "TASK-1", "--path", "todo/TASK-1.md", "--owner", "tester", "--request-id", request, "--expected-sha256", digest, "--json"}
	if code, out, diagnostics := runRepairCLI(t, args...); code != 1 || out != "" || diagnostics == "" {
		t.Fatalf("missing adoption code=%d out=%q stderr=%q", code, out, diagnostics)
	}
	args = append(args, "--adopt")
	code, first, diagnostics := runRepairCLI(t, args...)
	if code != 0 || diagnostics != "" || !json.Valid([]byte(first)) {
		t.Fatalf("adopt code=%d out=%q stderr=%q", code, first, diagnostics)
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(first), &receipt); err != nil || receipt["requestId"] != request || receipt["id"] != "TASK-1" || receipt["path"] != "todo/TASK-1.md" {
		t.Fatalf("receipt=%s err=%v", first, err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Contains(got, []byte("| **Status** | [ ] Pending |")) || bytes.Contains(got, []byte("[x] Done")) {
		t.Fatalf("status not repaired: err=%v bytes=%q", err, got)
	}
	if code, replay, diagnostics := runRepairCLI(t, args...); code != 0 || replay != first || diagnostics != "" {
		t.Fatalf("replay code=%d replay=%q first=%q stderr=%q", code, replay, first, diagnostics)
	}
	resume := append(removeFlag(args, "--adopt"), "--resume")
	if code, replay, diagnostics := runRepairCLI(t, resume...); code != 0 || replay != first || diagnostics != "" {
		t.Fatalf("resume code=%d replay=%q first=%q stderr=%q", code, replay, first, diagnostics)
	}
	conflict := replaceFlag(args, "--expected-sha256", strings.Repeat("d", 64))
	if code, out, diagnostics := runRepairCLI(t, conflict...); code != 1 || out != "" || diagnostics == "" {
		t.Fatalf("conflict code=%d out=%q stderr=%q", code, out, diagnostics)
	}
	wrongHash := replaceFlag(replaceFlag(args, "--expected-sha256", strings.Repeat("0", 64)), "--request-id", strings.Repeat("e", 32))
	if code, out, diagnostics := runRepairCLI(t, wrongHash...); code != 1 || out != "" || diagnostics == "" {
		t.Fatalf("wrong hash code=%d out=%q stderr=%q", code, out, diagnostics)
	}
}

func TestRepairStatusCLINoopMissingCellAndOutputRetry(t *testing.T) {
	dir, _, original := repairFixture(t)
	withoutCell := original
	digest := repairDigest(withoutCell)
	args := []string{"repair-status", "--dir", dir, "--id", "TASK-1", "--path", "todo/TASK-1.md", "--owner", "tester", "--request-id", strings.Repeat("f", 32), "--expected-sha256", digest, "--adopt", "--json"}
	if code, out, diagnostics := runRepairCLI(t, args...); code != 0 || diagnostics != "" || !json.Valid([]byte(out)) {
		t.Fatalf("missing-cell repair code=%d out=%q stderr=%q", code, out, diagnostics)
	}
	if code, out, diagnostics := runRepairCLI(t, args...); code != 0 || diagnostics != "" || out == "" {
		t.Fatalf("missing-cell replay code=%d out=%q stderr=%q", code, out, diagnostics)
	}

	dir2, path2, original2 := repairFixture(t)
	baseline2 := canonicalStatusBytes(t, original2)
	mutated := wrongStatusBytes(t, baseline2)
	if err := os.WriteFile(path2, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	args2 := []string{"repair-status", "--dir", dir2, "--id", "TASK-1", "--path", "todo/TASK-1.md", "--owner", "tester", "--request-id", strings.Repeat("1", 32), "--expected-sha256", repairDigest(mutated), "--adopt", "--json"}
	var diagnostics bytes.Buffer
	if code := run(args2, failClaimOutput{}, &diagnostics); code != 1 || diagnostics.Len() == 0 {
		t.Fatalf("output failure code=%d stderr=%s", code, diagnostics.String())
	}
	if code, out, diagnostics := runRepairCLI(t, args2...); code != 0 || diagnostics != "" || !json.Valid([]byte(out)) {
		t.Fatalf("output retry code=%d out=%q stderr=%q", code, out, diagnostics)
	}
}

func TestRepairStatusCLIRequiresHeldClaimTokenWhenClaimed(t *testing.T) {
	dir, path, original := repairFixture(t)
	baseline := canonicalStatusBytes(t, original)
	mutated := wrongStatusBytes(t, baseline)
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	if code, _, diagnostics := runRepairCLI(t, "claim", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", token, "--json"); code != 0 || diagnostics != "" {
		t.Fatalf("claim code=%d stderr=%s", code, diagnostics)
	}
	base := []string{"repair-status", "--dir", dir, "--id", "TASK-1", "--path", "todo/TASK-1.md", "--owner", "tester", "--request-id", strings.Repeat("2", 32), "--expected-sha256", repairDigest(mutated), "--adopt", "--json"}
	if code, out, diagnostics := runRepairCLI(t, base...); code != 1 || out != "" || diagnostics == "" {
		t.Fatalf("omitted token code=%d out=%q stderr=%q", code, out, diagnostics)
	}
	base = append(removeFlag(base, "--token"), "--token", token)
	if code, out, diagnostics := runRepairCLI(t, base...); code != 0 || diagnostics != "" || !json.Valid([]byte(out)) {
		t.Fatalf("exact token code=%d out=%q stderr=%q", code, out, diagnostics)
	}
}
