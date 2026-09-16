package githistory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBlobProtocolFailsClosed(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, raw := range []string{"", oid + " missing\n", oid + " blob -1\n", oid + " blob 16777217\n", oid + " blob 4\nab", oid + " blob 1\naX", strings.Repeat("b", 40) + " blob 0\n\n", oid + " blob 0\n\ntrailing"} {
		if _, err := parseBlobs([]byte(raw), []string{oid}); err == nil {
			t.Fatalf("invalid blob accepted %q", raw)
		}
	}
	if _, err := (&cappedBuffer{limit: 2}).Write([]byte("abc")); err == nil {
		t.Fatal("output limit ignored")
	}
}

func TestRefChangeDiscardsScanResult(t *testing.T) {
	dir := historyRepo(t)
	historyCard(t, dir, "tasks/todo/a.md", "id: TASK-1\n")
	head := historyCommit(t, dir)
	got, err := scanWithHook(context.Background(), dir, "tasks", func() error {
		gitTest(t, dir, "update-ref", "refs/heads/changed", head)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "refs changed") || len(got.IDs) != 0 {
		t.Fatalf("changed snapshot accepted: %+v %v", got, err)
	}
}

func TestSHA256History(t *testing.T) {
	dir := t.TempDir()
	gitTest(t, dir, "init", "--object-format=sha256", "-b", "fixture")
	gitTest(t, dir, "config", "commit.gpgsign", "false")
	historyCard(t, dir, "tasks/todo/a.md", "id: TASK-1\n")
	historyCommit(t, dir)
	got, err := Scan(context.Background(), dir, "tasks")
	if err != nil || len(got.Refs) != 1 || len(got.Refs[0].OID) != 64 || !reflect.DeepEqual(got.IDs, []string{"TASK-1"}) {
		t.Fatalf("sha256=%+v %v", got, err)
	}
}

func TestScanDoesNotRunConfiguredSignatureVerifier(t *testing.T) {
	dir := historyRepo(t)
	historyCard(t, dir, "tasks/todo/a.md", "id: TASK-1\n")
	historyCommit(t, dir)
	tree := gitTest(t, dir, "rev-parse", "HEAD^{tree}")
	commit := fmt.Sprintf("tree %s\nauthor Synthetic <s@example.invalid> 1 +0000\ncommitter Synthetic <s@example.invalid> 1 +0000\ngpgsig -----BEGIN PGP SIGNATURE-----\n synthetic\n -----END PGP SIGNATURE-----\n\nSynthetic\n", tree)
	cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(commit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic object: %v %s", err, out)
	}
	gitTest(t, dir, "update-ref", "refs/heads/signed", strings.TrimSpace(string(out)))
	marker := filepath.Join(dir, "invoked")
	script := filepath.Join(dir, "verifier")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "config", "gpg.program", script)
	gitTest(t, dir, "config", "log.showSignature", "true")
	probe := exec.Command("git", "--no-pager", "log", "-1", "--show-signature", "signed")
	probe.Dir = dir
	_, _ = probe.CombinedOutput()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("verifier fixture did not execute: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(context.Background(), dir, "tasks"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("scanner ran verifier: %v", err)
	}
}

func TestScanMergeParentsAndCancelledContext(t *testing.T) {
	dir := historyRepo(t)
	historyCard(t, dir, "tasks/todo/base.md", "id: TASK-1\n")
	historyCommit(t, dir)
	gitTest(t, dir, "checkout", "-b", "side")
	historyCard(t, dir, "tasks/todo/side.md", "id: TASK-3\n")
	historyCommit(t, dir)
	gitTest(t, dir, "checkout", "fixture")
	historyCard(t, dir, "tasks/todo/main.md", "id: TASK-2\n")
	historyCommit(t, dir)
	gitTest(t, dir, "merge", "--no-ff", "side", "-m", "merge fixture")
	gitTest(t, dir, "branch", "-d", "side")
	gitTest(t, dir, "rm", "tasks/todo/side.md", "tasks/todo/main.md")
	historyCommit(t, dir)
	got, err := Scan(context.Background(), dir, "tasks")
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"TASK-1", "TASK-2", "TASK-3"}) {
		t.Fatalf("merge history=%v %v", got.IDs, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, dir, "tasks"); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
}

func TestScanRejectsNonCommitTagAndSymlinkCard(t *testing.T) {
	dir := historyRepo(t)
	historyCard(t, dir, "tasks/todo/a.md", "id: TASK-1\n")
	historyCommit(t, dir)
	blob := gitTest(t, dir, "rev-parse", "HEAD:tasks/todo/a.md")
	gitTest(t, dir, "tag", "blob-tag", blob)
	if _, err := Scan(context.Background(), dir, "tasks"); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("blob tag=%v", err)
	}
	gitTest(t, dir, "tag", "-d", "blob-tag")
	if err := os.Symlink("a.md", filepath.Join(dir, "tasks/todo/link.md")); err != nil {
		t.Fatal(err)
	}
	historyCommit(t, dir)
	if _, err := Scan(context.Background(), dir, "tasks"); err == nil || !strings.Contains(err.Error(), "not regular") {
		t.Fatalf("symlink=%v", err)
	}
}
