package taskflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveContractRefusalsAndPreservation(t *testing.T) {
	root := t.TempDir()
	issue := filepath.Join(root, "tasks", "issue", "001-broken.md")
	original := []byte(`---
id: ISSUE-1
title: Broken widget
type: bug
status: open
severity: medium
disposition: blocked
---

## Summary

Keep this body.
`)
	if err := writeFileForTest(root, "tasks/issue/001-broken.md", original); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/todo/004-fix.md", []byte("---\nid: TASK-4\ntitle: Fix\ntype: chore\n---\n\nbody\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/issue/002-origin.md", []byte("---\nid: ISSUE-2\ntitle: Origin\ntype: bug\n---\n\norigin\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/_archive/todo/009-old.md", []byte("---\nid: TASK-9\ntitle: Old\ntype: chore\n---\n\nold\n")); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 10, 12, 3, 4, 5, 0, time.UTC)
	ctx := context.Background()

	if _, err := Resolve(ctx, root, ResolveRequest{Path: filepath.Join(root, "missing.md"), Resolution: "nope"}); err == nil || !strings.Contains(err.Error(), `invalid resolution "nope"`) {
		t.Fatalf("resolution check first: %v", err)
	}
	if _, err := Resolve(ctx, root, ResolveRequest{Path: filepath.Join(root, "tasks/issue/missing.md"), Resolution: "fixed"}); err == nil || !strings.Contains(err.Error(), "file not found:") {
		t.Fatalf("missing file: %v", err)
	}
	todo := filepath.Join(root, "tasks/todo/004-fix.md")
	if _, err := Resolve(ctx, root, ResolveRequest{Path: todo, Resolution: "fixed"}); err == nil || !strings.Contains(err.Error(), "is not under issue/") {
		t.Fatalf("zone: %v", err)
	}

	before, err := os.ReadFile(issue)
	if err != nil {
		t.Fatal(err)
	}
	for _, resolution := range []string{"wontfix", "invalid"} {
		_, err := Resolve(ctx, root, ResolveRequest{Path: issue, Resolution: resolution, By: "TASK-4", BySet: true, Now: when})
		if err == nil || !strings.Contains(err.Error(), "--by is not accepted with "+resolution) {
			t.Fatalf("%s --by: %v", resolution, err)
		}
	}
	after, err := os.ReadFile(issue)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected --by mutated the issue")
	}

	got, err := Resolve(ctx, root, ResolveRequest{Path: issue, Resolution: "fixed", Now: when})
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Resolved as fixed." || got.ResolvedAt != "2026-10-12T03:04:05Z" {
		t.Fatalf("result %+v", got)
	}
	text := string(mustRead(t, issue))
	if !strings.Contains(text, "resolution: fixed\n") || !strings.Contains(text, `resolution-summary: "Resolved as fixed."`) {
		t.Fatalf("stamp:\n%s", text)
	}
	if strings.Contains(text, "Resolved:") || !strings.Contains(text, "title: Broken widget") || !strings.Contains(text, "disposition: blocked") || !strings.Contains(text, "Keep this body.") {
		t.Fatalf("source not preserved:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks/done/001-broken.md")); !os.IsNotExist(err) {
		t.Fatal("resolve moved the issue")
	}

	if _, err := Resolve(ctx, root, ResolveRequest{Path: issue, Resolution: "fixed", By: "ISSUE-2", BySet: true}); err == nil || !strings.Contains(err.Error(), "not a TASK card") {
		t.Fatalf("fixed by issue: %v", err)
	}
	got, err = Resolve(ctx, root, ResolveRequest{Path: issue, Resolution: "fixed", By: "file:TASK-9", BySet: true, Now: when})
	if err != nil || got.Summary != "Resolved as fixed by TASK-9." {
		t.Fatalf("archive task by: %v %+v", err, got)
	}
	if _, err := Resolve(ctx, root, ResolveRequest{Path: issue, Resolution: "duplicate", By: "TASK-4", BySet: true}); err == nil || !strings.Contains(err.Error(), "not an ISSUE card") {
		t.Fatalf("duplicate by task: %v", err)
	}
	got, err = Resolve(ctx, root, ResolveRequest{Path: issue, Resolution: "duplicate", By: "ISSUE-2", BySet: true, Now: when.Add(time.Minute)})
	if err != nil || got.Summary != "Resolved as duplicate by ISSUE-2." {
		t.Fatalf("duplicate: %v %+v", err, got)
	}
	text = string(mustRead(t, issue))
	if strings.Count(text, "\nresolution:") != 1 || strings.Contains(text, "resolution: fixed") || !strings.Contains(text, "resolution: duplicate") {
		t.Fatalf("re-resolve did not overwrite:\n%s", text)
	}
	if _, err := Resolve(ctx, root, ResolveRequest{Path: issue, Resolution: "duplicate", BySet: true, Now: when}); err == nil || !strings.Contains(err.Error(), "--by needs an ISSUE id") {
		t.Fatalf("empty by: %v", err)
	}

	bare := filepath.Join(root, "tasks/issue/003-bare.md")
	if err := os.WriteFile(bare, []byte("no frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, root, ResolveRequest{Path: bare, Resolution: "wontfix", Now: when}); err == nil || !strings.Contains(err.Error(), "no frontmatter") {
		t.Fatalf("bare: %v", err)
	}
}

func TestResolveProjectionSnapshot(t *testing.T) {
	root := t.TempDir()
	issueRel := "tasks/issue/001-broken.md"
	issue := []byte("---\nid: ISSUE-1\ntitle: Broken\ntype: bug\n---\n\nbody\n")
	if err := writeFileForTest(root, issueRel, issue); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/live.md", []byte("live-bytes\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "ce-tasks.yaml", []byte("task-projection:\n  marker: tasks/live.md\n")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(issueRel))
	if _, err := Resolve(context.Background(), root, ResolveRequest{Path: path, Resolution: "fixed"}); err == nil || !strings.Contains(err.Error(), "cannot snapshot git index") {
		t.Fatalf("snapshot before git: %v", err)
	}
	if !bytes.Equal(mustRead(t, path), issue) {
		t.Fatal("failed snapshot mutated the issue")
	}
	gitInitForTest(t, root)
	owned := filepath.Join(root, "ce-tasks.yaml")
	if err := os.WriteFile(owned, []byte("task-projection:\n  marker: "+filepath.ToSlash(issueRel)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), root, ResolveRequest{Path: path, Resolution: "wontfix"}); err == nil || !strings.Contains(err.Error(), "refusing lifecycle mutation of marker-owned projection: "+path) {
		t.Fatalf("owned: %v", err)
	}
	if !bytes.Equal(mustRead(t, path), issue) {
		t.Fatal("owned path was written")
	}

	if err := os.WriteFile(owned, []byte("task-projection:\n  marker: tasks/live.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := snapshotProjection(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks/live.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snap.indexPath, []byte("not-an-index"), snap.indexMode); err != nil {
		t.Fatal(err)
	}
	if err := snap.restore(); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(root, "tasks/live.md"))) != "live-bytes\n" {
		t.Fatal("tasks tree restore failed")
	}
	if !bytes.Equal(mustRead(t, snap.indexPath), snap.index) {
		t.Fatal("git index restore failed")
	}

	when := time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC)
	if _, err := Resolve(context.Background(), root, ResolveRequest{Path: path, Resolution: "invalid", Now: when}); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(root, "tasks/live.md"))) != "live-bytes\n" {
		t.Fatal("sibling changed")
	}
	if !bytes.Equal(mustRead(t, snap.indexPath), snap.index) {
		t.Fatal("index changed on success")
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), root, ResolveRequest{Path: path, Resolution: "fixed", Now: when}); err == nil {
		t.Fatal("unwritable card succeeded")
	}
	if !bytes.Contains(mustRead(t, path), []byte("resolution: invalid")) {
		t.Fatal("failed write lost the previous resolution")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
