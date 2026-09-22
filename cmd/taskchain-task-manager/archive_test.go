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

func TestArchiveCLIAdoptOutputFailureAndReplay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(dir, taskstore.CreateRequest{Title: "archive"}); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-1\ntitle: archive\nreview-result: pass\nreview-proof: independent review\n---\n")
	if err := os.MkdirAll(filepath.Join(dir, "done"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "todo/TASK-1.md"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo/TASK-1.md"), filepath.Join(dir, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(t.TempDir(), "archive.yaml")
	config := []byte("schema-version: 1\narchive-admission:\n  fields:\n    review: review-result\n    evidence: review-proof\n    resolution: disposition\n    promoted-to: promoted\n    children: child-ids\n  accepted-reviews: [pass]\n")
	if err := os.WriteFile(rules, config, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"archive", "--dir", dir, "--id", "TASK-1", "--source", "done/TASK-1.md", "--rules", rules, "--owner", "worker", "--request-id", strings.Repeat("a", 32), "--expected-sha256", repairDigest(raw), "--json"}
	var out, diagnostics bytes.Buffer
	if code := run(args, &out, &diagnostics); code != 1 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "--adopt") {
		t.Fatalf("missing adoption code=%d out=%s err=%s", code, &out, &diagnostics)
	}
	diagnostics.Reset()
	if code := run(append(append([]string{}, args...), "--adopt"), failClaimOutput{}, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "retry the identical request") {
		t.Fatalf("output failure=%d %s", code, &diagnostics)
	}
	stored, err := os.ReadFile(filepath.Join(dir, ".task-manager-archives.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--adopt", "--resume"} {
		out.Reset()
		diagnostics.Reset()
		if code := run(append(append([]string{}, args...), flag), &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
			t.Fatalf("replay=%d %s", code, &diagnostics)
		}
		// The version key now arrives on the emitted document, not on the
		// result type, so the decode target embeds the type and names the key.
		var result struct {
			taskstore.ArchiveResult
			OutputVersion int `json:"outputVersion"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.OutputVersion != 1 || result.Status != "completed" || !result.CompletionEligible {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		after, err := os.ReadFile(filepath.Join(dir, ".task-manager-archives.json"))
		if err != nil || !bytes.Equal(after, stored) {
			t.Fatalf("replay changed journal: %v", err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "_archive/done/TASK-1.md"))
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatal("archive bytes lost:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "done/TASK-1.md")); !os.IsNotExist(err) {
		t.Fatal("source remains:", err)
	}
}

func TestArchiveCLIHelpAndUsage(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := run([]string{"archive", "--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), archiveUsage) || diagnostics.Len() != 0 {
		t.Fatalf("help %d %s %s", code, &out, &diagnostics)
	}
	out.Reset()
	if code := run([]string{"archive", "--json"}, &out, &diagnostics); code != 2 || out.Len() != 0 {
		t.Fatalf("usage %d %s", code, &out)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), archiveUsage) {
		t.Fatal("top-level help missing archive")
	}
}
