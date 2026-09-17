package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func capacityCLIFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(dir, taskstore.CreateRequest{ID: "TASK-1", Title: "archive"}); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-1\ntitle: archive\nreview-result: pass\nreview-proof: checked\n---\n")
	if err := os.Mkdir(filepath.Join(dir, "done"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "todo/TASK-1.md"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo/TASK-1.md"), filepath.Join(dir, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	rules := []byte("schema-version: 1\narchive-admission:\n  fields:\n    review: review-result\n    evidence: review-proof\n    resolution: disposition\n    promoted-to: promoted\n    children: child-ids\n  accepted-reviews: [pass]\n")
	req := taskstore.ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("a", 32), Source: "done/TASK-1.md", ExpectedSHA256: repairDigest(raw), Operation: "archive", Rules: rules}
	if _, err := taskstore.Archive(dir, req, true); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestArchiveCapacityCLIAdoptResumeJSONAndOutputFailure(t *testing.T) {
	dir := capacityCLIFixture(t)
	id := strings.Repeat("b", 32)
	base := []string{"adopt-archive-capacity", "--dir", dir, "--upgrade-id", id, "--json"}
	var out, diagnostics bytes.Buffer
	if code := run(append(append([]string{}, base...), "--adopt"), &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("adopt code=%d out=%s err=%s", code, &out, &diagnostics)
	}
	var result taskstore.ArchiveCapacityResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.UpgradeID != id || result.Status != "completed" || result.StorageProtocol != 5 || result.JournalSchema != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run(append(append([]string{}, base...), "--adopt"), &out, &diagnostics); code != 1 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "--resume") {
		t.Fatalf("repeat adopt code=%d out=%s err=%s", code, &out, &diagnostics)
	}
	diagnostics.Reset()
	if code := run(append(append([]string{}, base...), "--resume"), failClaimOutput{}, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "may already be complete") {
		t.Fatalf("output failure code=%d err=%s", code, &diagnostics)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run(append(append([]string{}, base...), "--resume"), &out, &diagnostics); code != 0 || diagnostics.Len() != 0 || !json.Valid(out.Bytes()) {
		t.Fatalf("resume code=%d out=%s err=%s", code, &out, &diagnostics)
	}
}

func TestArchiveCapacityCLIHelpUsageAndExactUpgradeID(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := run([]string{"adopt-archive-capacity", "--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), archiveCapacityUsage) || diagnostics.Len() != 0 {
		t.Fatalf("help code=%d out=%s err=%s", code, &out, &diagnostics)
	}
	out.Reset()
	if code := run([]string{"adopt-archive-capacity", "--json"}, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatalf("usage code=%d out=%s err=%s", code, &out, &diagnostics)
	}
	dir := capacityCLIFixture(t)
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"adopt-archive-capacity", "--dir", dir, "--upgrade-id", "ABC", "--adopt", "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "upgrade ID") {
		t.Fatalf("id code=%d out=%s err=%s", code, &out, &diagnostics)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), archiveCapacityUsage) {
		t.Fatal("top-level help missing archive capacity")
	}
}
