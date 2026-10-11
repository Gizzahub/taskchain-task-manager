package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardruntime"
)

func writeMaintenanceFixture(t *testing.T, base string) (string, []byte) {
	t.Helper()
	initGitRepo(t)
	policy := []byte("authority\n")
	writeCardFile(t, "policy/authority.md", string(policy))
	card := []byte("---\nid: TASK-1\ntitle: Keep\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nKeep.\n")
	writeCardFile(t, "tasks/todo/001-card.md", string(card))
	writeCardFile(t, "ce-tasks.yaml", "task-maintenance:\n  policy-path: policy/authority.md\n")
	if base == "" {
		base = gitOut(t, "rev-parse", "HEAD")
	}
	branch := gitOut(t, "branch", "--show-current")
	digest := fmt.Sprintf("%x", sha256.Sum256(policy))
	cardDigest := fmt.Sprintf("%x", sha256.Sum256(card))
	binding := boardruntime.MaintenanceBinding{Identity: "policy", Version: "1", Digest: digest, Path: "policy/authority.md"}
	file := func(path, kind, sum string) boardruntime.MaintenanceFile {
		return boardruntime.MaintenanceFile{Path: path, Kind: kind, Digest: sum}
	}
	cardRow := boardruntime.MaintenanceCard{
		Identity: "TASK-1", Path: "tasks/todo/001-card.md", Scope: "keep", Priority: "P2", State: "todo",
	}
	plan := boardruntime.MaintenancePlan{
		Version: 1,
		Repository: boardruntime.MaintenanceRepository{
			ID: "fixture", Branch: branch, Base: base,
		},
		Board:        "tasks",
		Controller:   boardruntime.MaintenanceBinding{Identity: "controller", Version: "1", Digest: digest},
		Policy:       binding,
		Rule:         boardruntime.MaintenanceBinding{Identity: "rule", Version: "1", Digest: digest, Path: binding.Path},
		SourceSchema: boardruntime.MaintenanceBinding{Identity: "source-schema", Version: "1", Digest: digest, Path: binding.Path},
		TargetSchema: boardruntime.MaintenanceBinding{Identity: "target-schema", Version: "1", Digest: digest, Path: binding.Path},
		Before: []boardruntime.MaintenanceFile{
			file("policy/authority.md", "reference", digest),
			file("tasks/todo/001-card.md", "card", cardDigest),
		},
		After: []boardruntime.MaintenanceFile{
			file("policy/authority.md", "reference", digest),
			file("tasks/todo/001-card.md", "card", cardDigest),
		},
		Mappings:    []boardruntime.MaintenanceMapping{},
		BeforeCards: []boardruntime.MaintenanceCard{cardRow},
		AfterCards:  []boardruntime.MaintenanceCard{cardRow},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("plan.json", append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return "plan.json", card
}

func TestMaintenanceRefusals(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		t.Chdir(t.TempDir())
		initGitRepo(t)
		for _, body := range []string{"[]\n", "{}\n", "{\"version\":1,\"version\":1}\n", "not-json\n"} {
			if err := os.WriteFile("plan.json", []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			var out, diag bytes.Buffer
			code := runTask([]string{"maintenance", "plan.json"}, &out, &diag)
			if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "decode maintenance plan") {
				t.Fatalf("body=%q code=%d stdout=%q stderr=%q", body, code, out.String(), diag.String())
			}
		}
	})

	t.Run("policy missing", func(t *testing.T) {
		t.Chdir(t.TempDir())
		plan, _ := writeMaintenanceFixture(t, "")
		if err := os.Remove("ce-tasks.yaml"); err != nil {
			t.Fatal(err)
		}
		var out, diag bytes.Buffer
		code := runTask([]string{"maintenance", plan}, &out, &diag)
		if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "task-maintenance policy is not declared in ce-tasks.yaml") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
		}
	})

	t.Run("unresolved execution", func(t *testing.T) {
		t.Chdir(t.TempDir())
		initGitRepo(t)
		writeRuntimeFile(t, "executions.json", `[{"task":"TASK-9","type":"feat","source":"source","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"work","worktree":"primary","startedAt":"2026-10-11T00:00:00Z","updatedAt":"2026-10-11T00:00:00Z"}]`+"\n")
		if err := os.WriteFile("plan.json", []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var out, diag bytes.Buffer
		code := runTask([]string{"maintenance", "plan.json"}, &out, &diag)
		if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "active or unresolved runtime execution: TASK-9") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
		}
		assertNoMutationLock(t)
	})

	t.Run("stale head", func(t *testing.T) {
		t.Chdir(t.TempDir())
		plan, card := writeMaintenanceFixture(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		before := worktreeFiles(t)
		var out, diag bytes.Buffer
		code := runTask([]string{"maintenance", plan}, &out, &diag)
		if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "repository base revision stale") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
		}
		assertSameFiles(t, before, worktreeFiles(t))
		if mustRead(t, "tasks/todo/001-card.md") != string(card) {
			t.Fatal("stale plan changed the card")
		}
	})
}

func TestMaintenancePreviewPreservesBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	plan, card := writeMaintenanceFixture(t, "")
	before := worktreeFiles(t)
	var out, diag bytes.Buffer
	code := runTask([]string{"maintenance", "plan", plan}, &out, &diag)
	if code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), "Board maintenance [Preview]:") || !strings.Contains(out.String(), "Plan Digest:") || strings.Contains(out.String(), "Receipt:") {
		t.Fatalf("stdout=%s", out.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))
	assertNoMutationLock(t)
	if mustRead(t, "tasks/todo/001-card.md") != string(card) {
		t.Fatal("preview changed the card")
	}
}

func TestMaintenancePostExchangeExitsThree(t *testing.T) {
	t.Chdir(t.TempDir())
	plan, card := writeMaintenanceFixture(t, "")
	t.Cleanup(func() { boardruntime.TestingAfterExchange = nil })
	boardruntime.TestingAfterExchange = func() error { return errors.New("probe failed") }

	var out, diag bytes.Buffer
	code := runTask([]string{"maintenance", "--apply", "--json", plan}, &out, &diag)
	if code != 3 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(diag.String(), "applied, do not rerun:") || !strings.Contains(diag.String(), "board maintenance committed") || !strings.Contains(diag.String(), "probe failed") {
		t.Fatalf("stderr=%s", diag.String())
	}
	if !strings.Contains(out.String(), `"committed": true`) || !strings.Contains(out.String(), `"receiptPath": "tasks/.ce-maintenance-receipt.json"`) {
		t.Fatalf("stdout=%s", out.String())
	}
	if mustRead(t, "tasks/todo/001-card.md") != string(card) {
		t.Fatal("exchanged card bytes changed")
	}
	receipt := mustRead(t, "tasks/.ce-maintenance-receipt.json")
	if !strings.Contains(receipt, `"plan"`) || !strings.Contains(receipt, `"policy": "policy"`) {
		t.Fatalf("receipt=%s", receipt)
	}
	matches, err := filepath.Glob(".ce-board-stage-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("stage=%v %v", matches, err)
	}
	if _, err := os.Stat(filepath.Join(matches[0], ".ce-maintenance-receipt.json")); !os.IsNotExist(err) {
		t.Fatal("old board contains the receipt")
	}
	assertNoMutationLock(t)
}
