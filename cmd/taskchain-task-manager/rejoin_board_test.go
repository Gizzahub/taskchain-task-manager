package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func rejoinGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// rejoinCLIFixture builds a synthetic shared board and a second worktree that
// will return as the owner.  Both share one common directory, so this is the
// same-common mode.
func rejoinCLIFixture(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	rejoinGit(t, repo, "init", "-b", "fixture")
	rejoinGit(t, repo, "config", "commit.gpgsign", "false")
	board := filepath.Join(repo, "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "archive"}); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-1\ntitle: archive\nreview-result: pass\nreview-proof: checked\n---\n")
	rules := []byte("schema-version: 1\narchive-admission:\n  fields:\n    review: review-result\n    evidence: review-proof\n    resolution: disposition\n    promoted-to: promoted\n    children: child-ids\n  accepted-reviews: [pass]\n")
	if err := os.MkdirAll(filepath.Join(board, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(board, "todo/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(board, "done/TASK-1.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	req := taskstore.ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("a", 32), Source: "done/TASK-1.md", ExpectedSHA256: repairDigest(raw), Operation: "archive", Rules: rules}
	if _, err := taskstore.Archive(board, req, true); err != nil {
		t.Fatal(err)
	}
	if err := taskstore.AdoptArchiveCapacity(board, strings.Repeat("c", 32)); err != nil {
		t.Fatal(err)
	}
	rejoinGit(t, repo, "add", "-A", "tasks")
	rejoinGit(t, repo, "commit", "-m", "synthetic shared board")
	if _, err := taskstore.EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	// The returning worktree is created after sharing is enabled, so it is not
	// yet a participant, and nothing commits afterwards: the common state binds
	// the source participant's HEAD and the rejoin plan must name that same one.
	linked := filepath.Join(t.TempDir(), "returning")
	rejoinGit(t, repo, "worktree", "add", "--detach", linked, "HEAD")
	target := filepath.Join(linked, "tasks")
	// The control journals are gitignored, so the returning worktree receives
	// the source's own bytes; replacing them with the rejoined targets is
	// exactly what the transaction under test does.
	for _, name := range []string{
		".task-manager-ids.json", ".task-manager-transitions.json", ".task-manager-repairs.json",
		".task-manager-relocations.json", ".task-manager-archives.json", ".task-manager-archive-capacity.json",
	} {
		copyRejoinFile(t, filepath.Join(board, name), filepath.Join(target, name))
	}
	entries, err := os.ReadDir(board)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".task-manager-owner-rejoin-") || strings.HasPrefix(entry.Name(), ".task-manager-archive-capacity-") {
			copyRejoinFile(t, filepath.Join(board, entry.Name()), filepath.Join(target, entry.Name()))
		}
	}
	copyRejoinDir(t, filepath.Join(board, "archive"), filepath.Join(target, "archive"))
	if err := os.RemoveAll(filepath.Join(target, "todo")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	return board, target
}

func copyRejoinFile(t *testing.T, from, to string) {
	t.Helper()
	info, err := os.Lstat(from)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(to, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func copyRejoinDir(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		return
	}
	for _, entry := range entries {
		src, dst := filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())
		if entry.IsDir() {
			copyRejoinDir(t, src, dst)
			continue
		}
		copyRejoinFile(t, src, dst)
	}
}

func TestRejoinBoardCLIUsageAndStatusOnAnUntouchedBoard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(dir); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"rejoin-board", "--help"}, &out, &diagnostics); code != 0 || out.Len() == 0 || diagnostics.Len() != 0 {
		t.Fatalf("help code=%d err=%s", code, &diagnostics)
	}
	// Every rejected invocation must leave stdout empty: stdout carries the
	// requested data only, so a usage error is never mistaken for a result.
	for _, args := range [][]string{
		{"rejoin-board", "--dir", dir, "--json"},
		{"rejoin-board", "--dir", dir, "--status"},
		{"rejoin-board", "--dir", dir, "--status", "--apply", "--json"},
		{"rejoin-board", "--status", "--json"},
		{"rejoin-board", "--dir", dir, "--status", "--json", "stray"},
		{"rejoin-board", "--dir", dir, "--apply", "--json"},
		{"rejoin-board", "--dir", dir, "--prepare", "--json", "--source", dir},
		{"rejoin-board", "--dir", dir, "--prepare", "--json", "--floor", "TASK"},
	} {
		out.Reset()
		diagnostics.Reset()
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatalf("%v: code=%d out=%s", args, code, &out)
		}
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"rejoin-board", "--dir", dir, "--status", "--json"}, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("status code=%d err=%s", code, &diagnostics)
	}
	var result taskstore.RejoinBoardResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Phase != "absent" {
		t.Fatalf("status on an untouched board: %v %+v", err, result)
	}
}

func TestRejoinBoardCLIPrepareApplyStatusSameCommon(t *testing.T) {
	source, target := rejoinCLIFixture(t)
	work := t.TempDir()
	planPath := filepath.Join(work, "plan.json")
	payloadPath := filepath.Join(work, "payload.bin")
	id := strings.Repeat("d", 32)

	var out, diagnostics bytes.Buffer
	code := run([]string{"rejoin-board", "--dir", target, "--prepare", "--json", "--source", source,
		"--rejoin-id", id, "--source-fenced", "--plan", planPath, "--payload", payloadPath}, &out, &diagnostics)
	if code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("prepare code=%d err=%s", code, &diagnostics)
	}
	var prepared taskstore.RejoinBoardResult
	if err := json.Unmarshal(out.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Mode != "same-common" || prepared.Phase != "prepared" || prepared.RejoinID != id {
		t.Fatalf("prepared: %+v", prepared)
	}
	if prepared.SourceNamespace != prepared.TargetNamespace || prepared.TargetStorageProtocol != 6 {
		t.Fatalf("same-common prepare changed the namespace or protocol: %+v", prepared)
	}

	// A prepare publishes nothing, so the board must still report no rejoin.
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"rejoin-board", "--dir", target, "--status", "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("status after prepare code=%d err=%s", code, &diagnostics)
	}
	var mid taskstore.RejoinBoardResult
	if err := json.Unmarshal(out.Bytes(), &mid); err != nil || mid.Phase != "absent" {
		t.Fatalf("prepare published something: %+v", mid)
	}

	applyArgs := []string{"rejoin-board", "--dir", target, "--apply", "--json", "--plan", planPath, "--payload", payloadPath}
	out.Reset()
	diagnostics.Reset()
	if code := run(applyArgs, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("apply code=%d err=%s", code, &diagnostics)
	}
	var applied taskstore.RejoinBoardResult
	if err := json.Unmarshal(out.Bytes(), &applied); err != nil || applied.Phase != "completed" || applied.PlanSHA256 != prepared.PlanSHA256 {
		t.Fatalf("applied: %v %+v", err, applied)
	}
	appliedRaw := append([]byte(nil), out.Bytes()...)

	// Apply is the resume: repeating it with the identical plan must reach the
	// same completed state rather than a second, different one.
	out.Reset()
	diagnostics.Reset()
	if code := run(applyArgs, &out, &diagnostics); code != 0 {
		t.Fatalf("resume code=%d err=%s", code, &diagnostics)
	}
	if !bytes.Equal(out.Bytes(), appliedRaw) {
		t.Fatalf("resume reached a different state: %s", &out)
	}

	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"rejoin-board", "--dir", target, "--status", "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("status code=%d err=%s", code, &diagnostics)
	}
	if !bytes.Equal(out.Bytes(), appliedRaw) {
		t.Fatalf("status disagrees with apply: %s", &out)
	}
}

func TestRejoinBoardCLIRefusesTamperedAndUnfencedInput(t *testing.T) {
	source, target := rejoinCLIFixture(t)
	work := t.TempDir()
	planPath := filepath.Join(work, "plan.json")
	payloadPath := filepath.Join(work, "payload.bin")
	id := strings.Repeat("d", 32)

	var out, diagnostics bytes.Buffer
	// The writer fence is an operator assertion, and an unasserted one is not a
	// default that can be quietly supplied.
	if code := run([]string{"rejoin-board", "--dir", target, "--prepare", "--json", "--source", source,
		"--rejoin-id", id, "--plan", planPath, "--payload", payloadPath}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("unfenced prepare accepted: code=%d out=%s", code, &out)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"rejoin-board", "--dir", target, "--prepare", "--json", "--source", source,
		"--rejoin-id", id, "--source-fenced", "--plan", planPath, "--payload", payloadPath}, &out, &diagnostics); code != 0 {
		t.Fatalf("prepare code=%d err=%s", code, &diagnostics)
	}
	payload, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-1] ^= 0xff
	if err := os.WriteFile(payloadPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"rejoin-board", "--dir", target, "--apply", "--json", "--plan", planPath, "--payload", payloadPath}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatalf("tampered payload accepted: code=%d out=%s", code, &out)
	}
	// A refused apply must leave the board exactly where it was.
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"rejoin-board", "--dir", target, "--status", "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("status code=%d err=%s", code, &diagnostics)
	}
	var after taskstore.RejoinBoardResult
	if err := json.Unmarshal(out.Bytes(), &after); err != nil || after.Phase != "absent" {
		t.Fatalf("a refused apply moved the board: %v %+v", err, after)
	}
}
