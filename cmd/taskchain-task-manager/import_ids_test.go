package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestImportRealGitDeletedCard(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid")
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, raw)
		}
		return string(raw)
	}
	git("init", "-b", "fixture")
	git("config", "commit.gpgsign", "false")
	if err := os.Mkdir(filepath.Join(repo, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tasks", "card.md"), []byte("id: TASK-090\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "synthetic card")
	git("rm", "tasks/card.md")
	git("commit", "-m", "synthetic deletion")
	before := git("status", "--porcelain=v1")
	target := filepath.Join(t.TempDir(), "target")
	if err := taskstore.Init(target); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"import-ids", "--repo", repo, "--preview", "--json"}, &out, &diag); code != 0 || !strings.Contains(out.String(), "TASK-90") {
		t.Fatalf("preview: %d %s %s", code, &out, &diag)
	}
	out.Reset()
	if code := run([]string{"import-ids", "--repo", repo, "--dir", target, "--json"}, &out, &diag); code != 0 {
		t.Fatalf("import: %d %s", code, &diag)
	}
	entry, err := taskstore.Create(target, taskstore.CreateRequest{Title: "after deleted history"})
	if err != nil || entry.Card.ID != "TASK-91" {
		t.Fatalf("reservation: %v %v", entry, err)
	}
	if git("status", "--porcelain=v1") != before {
		t.Fatal("source working tree changed")
	}
}

func TestImportScanFailureCannotMutateTarget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "target")
	if err := taskstore.Init(dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, ".task-manager-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	failing := func(context.Context, string, string) (githistory.Report, error) {
		return githistory.Report{IDs: []string{"TASK-999"}}, errors.New("synthetic incomplete history")
	}
	code := runImportIDsWithScan([]string{"import-ids", "--repo", "synthetic", "--dir", dir, "--json"}, &out, &diag, failing)
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "incomplete history") {
		t.Fatalf("result %d %s %s", code, &out, &diag)
	}
	after, err := os.ReadFile(filepath.Join(dir, ".task-manager-ids.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("partial scan changed reservations")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected artifacts: %v %v", entries, err)
	}
}

func TestImportPreviewAndUnionRetry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "target")
	if err := taskstore.Init(dir); err != nil {
		t.Fatal(err)
	}
	scan := func(context.Context, string, string) (githistory.Report, error) {
		return githistory.Report{Refs: []githistory.Ref{}, IDs: []string{"TASK-90", "PLAN-7"}}, nil
	}
	args := []string{"import-ids", "--repo", "synthetic", "--preview", "--json"}
	var out, diag bytes.Buffer
	if code := runImportIDsWithScan(args, &out, &diag, scan); code != 0 {
		t.Fatalf("preview %d %s", code, &diag)
	}
	e, err := taskstore.Create(dir, taskstore.CreateRequest{Title: "preview did not reserve"})
	if err != nil || e.Card.ID != "TASK-1" {
		t.Fatalf("preview mutation %v %v", e, err)
	}
	args = []string{"import-ids", "--repo", "synthetic", "--dir", dir, "--json"}
	if code := runImportIDsWithScan(args, failClaimOutput{}, &diag, scan); code != 1 {
		t.Fatalf("outputfailure=%d", code)
	}
	for range 2 {
		out.Reset()
		if code := runImportIDsWithScan(args, &out, &diag, scan); code != 0 {
			t.Fatalf("retry=%d %s", code, &diag)
		}
	}
	e, err = taskstore.Create(dir, taskstore.CreateRequest{Title: "after import"})
	if err != nil || e.Card.ID != "TASK-91" {
		t.Fatalf("import floor=%v %v", e, err)
	}
}

func TestImportUsageDoesNotScan(t *testing.T) {
	for _, args := range [][]string{{"import-ids", "--json"}, {"import-ids", "--repo", "x", "--json"}, {"import-ids", "--repo", "x", "--preview", "--dir", "x", "--json"}} {
		called := false
		scan := func(context.Context, string, string) (githistory.Report, error) {
			called = true
			return githistory.Report{}, nil
		}
		var out, diag bytes.Buffer
		if code := runImportIDsWithScan(args, &out, &diag, scan); code != 2 || called || out.Len() != 0 {
			t.Fatalf("usage %v %d called=%v", args, code, called)
		}
	}
}
