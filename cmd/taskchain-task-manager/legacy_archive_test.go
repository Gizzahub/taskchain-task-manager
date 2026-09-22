package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func legacyArchiveFixture(t *testing.T) (string, []string, []byte, os.FileMode) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if code, _, diag := runCLI(t, "init", "--dir", dir, "--json"); code != 0 {
		t.Fatal(diag)
	}
	if code, _, diag := runCLI(t, "create", "--dir", dir, "--title", "legacy", "--json"); code != 0 {
		t.Fatal(diag)
	}
	if code, _, diag := runCLI(t, "create", "--dir", dir, "--title", "dependent", "--depends-on", "TASK-1", "--json"); code != 0 {
		t.Fatal(diag)
	}
	source := filepath.Join(dir, "todo", "TASK-1.md")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "_archive", "done"), 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "_archive", "done", "TASK-1.md")
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(t.TempDir(), "archive.yaml")
	if err := os.WriteFile(rules, []byte(`schema-version: 1
archive-admission:
  fields:
    review: review-result
    evidence: review-proof
    resolution: disposition
    promoted-to: promoted
    children: child-ids
  accepted-reviews: [pass]
`), 0644); err != nil {
		t.Fatal(err)
	}
	base := []string{"adopt-legacy-archive", "--dir", dir, "--id", "TASK-1", "--source", "_archive/done/TASK-1.md", "--rules", rules, "--owner", "operator", "--request-id", strings.Repeat("a", 32), "--expected-sha256", digestBytes(raw), "--expected-mode", strconv.FormatUint(uint64(info.Mode().Perm()), 8), "--assertion", "legacy evidence", "--adopt", "--json"}
	return dir, base, raw, info.Mode()
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, diag bytes.Buffer
	code := run(args, &out, &diag)
	return code, out.String(), diag.String()
}

func digestBytes(raw []byte) string {
	return repairDigest(raw)
}

func TestLegacyArchiveCLIHelpAndUsage(t *testing.T) {
	if code, out, diag := runCLI(t, "adopt-legacy-archive", "--help"); code != 0 || !strings.Contains(out, "Usage: adopt-legacy-archive") || diag != "" {
		t.Fatalf("help code=%d out=%q diag=%q", code, out, diag)
	}
	dir := filepath.Join(t.TempDir(), "absent")
	if code, out, diag := runCLI(t, "adopt-legacy-archive", "--dir", dir, "--json"); code != 2 || out != "" || diag == "" {
		t.Fatalf("usage code=%d out=%q diag=%q", code, out, diag)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("usage created board: %v", err)
	}
	_, args, _, _ := legacyArchiveFixture(t)
	for _, caseArgs := range [][]string{
		replaceFlag(args, "--expected-mode", "0"),
		replaceFlag(args, "--expected-mode", "1000"),
		replaceFlag(args, "--expected-mode", "888"),
		replaceFlag(args, "--assertion", ""),
		append(append([]string(nil), args...), "--resume", "--adopt"),
	} {
		if code, out, diag := runCLI(t, caseArgs...); code != 2 || out != "" || diag == "" {
			t.Fatalf("invalid args=%v code=%d out=%q diag=%q", caseArgs, code, out, diag)
		}
	}
}

func TestLegacyArchiveCLIApprovalControlsDependencyCompletion(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(strconv.FormatBool(approve), func(t *testing.T) {
			dir, args, raw, mode := legacyArchiveFixture(t)
			beforeCode, beforeReady, beforeDiag := runCLI(t, "ready", "--dir", dir, "--json")
			if beforeCode != 0 || beforeDiag != "" {
				t.Fatalf("baseline ready code=%d diag=%q", beforeCode, beforeDiag)
			}
			var baseline struct {
				Entries []taskstore.Entry `json:"entries"`
			}
			if err := json.Unmarshal([]byte(beforeReady), &baseline); err != nil {
				t.Fatal(err)
			}
			if readyHasEntry(baseline.Entries, "TASK-2") {
				t.Fatal("dependent was ready before legacy adoption")
			}
			if approve {
				args = append(args, "--approve-completion")
			}
			code, out, diag := runCLI(t, args...)
			if code != 0 || diag != "" {
				t.Fatalf("adoption code=%d out=%q diag=%q", code, out, diag)
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(out), &result); err != nil || result["completionEligible"] != approve {
				t.Fatalf("result=%s err=%v", out, err)
			}
			readyCode, readyOut, readyDiag := runCLI(t, "ready", "--dir", dir, "--json")
			var readyEntries struct {
				Entries []taskstore.Entry `json:"entries"`
			}
			readyErr := json.Unmarshal([]byte(readyOut), &readyEntries)
			if readyCode != 0 || readyDiag != "" || readyErr != nil || (readyHasEntry(readyEntries.Entries, "TASK-2") != approve) {
				t.Fatalf("approval=%v ready code=%d out=%q diag=%q", approve, readyCode, readyOut, readyDiag)
			}
			got, err := os.ReadFile(filepath.Join(dir, "_archive", "done", "TASK-1.md"))
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("card changed: %v", err)
			}
			info, err := os.Stat(filepath.Join(dir, "_archive", "done", "TASK-1.md"))
			if err != nil || info.Mode() != mode {
				t.Fatalf("mode changed: %v", err)
			}
		})
	}
}

func readyHasEntry(entries []taskstore.Entry, id string) bool {
	for _, entry := range entries {
		if entry.Card.ID == id {
			return true
		}
	}
	return false
}

func TestLegacyArchiveCLIOutputFailureThenExactReplay(t *testing.T) {
	dir, args, raw, _ := legacyArchiveFixture(t)
	var diag bytes.Buffer
	if code := run(args, failClaimOutput{}, &diag); code != 1 || diag.Len() == 0 {
		t.Fatalf("output failure code=%d diag=%q", code, diag.String())
	}
	journalPath := filepath.Join(dir, ".task-manager-archives.json")
	journalBefore, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	resume := append(removeFlag(args, "--adopt"), "--resume")
	firstCode, first, firstDiag := runCLI(t, resume...)
	if firstCode != 0 || firstDiag != "" || !json.Valid([]byte(first)) {
		t.Fatalf("retry code=%d out=%q diag=%q", firstCode, first, firstDiag)
	}
	cardPath := filepath.Join(dir, "_archive", "done", "TASK-1.md")
	cardBefore, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	cardInfo, err := os.Stat(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	secondCode, second, secondDiag := runCLI(t, resume...)
	if secondCode != 0 || second != first || secondDiag != "" {
		t.Fatalf("replay code=%d out=%q first=%q diag=%q", secondCode, second, first, secondDiag)
	}
	thirdCode, third, thirdDiag := runCLI(t, resume...)
	if thirdCode != 0 || third != second || thirdDiag != "" {
		t.Fatalf("second replay code=%d out=%q second=%q diag=%q", thirdCode, third, second, thirdDiag)
	}
	journalAfter, err := os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(journalBefore, journalAfter) {
		t.Fatalf("replay changed journal: %v", err)
	}
	got, err := os.ReadFile(cardPath)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("replay changed card: %v", err)
	}
	gotInfo, err := os.Stat(cardPath)
	if err != nil || gotInfo.Mode() != cardInfo.Mode() || !bytes.Equal(cardBefore, got) {
		t.Fatalf("replay changed card metadata: %v", err)
	}
}
