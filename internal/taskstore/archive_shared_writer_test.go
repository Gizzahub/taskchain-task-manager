package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveSharedWriterRecovery(t *testing.T) {
	t.Parallel()
	for _, point := range []string{"after-archive-common-pending", "after-archive-journal", "after-target", "after-source", "after-archive-receipt"} {
		t.Run(point, func(t *testing.T) {
			_, board, other := sharedFixture(t)
			if _, err := EnableShared(board, false); err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(board, "todo/TASK-1.md")
			raw, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: checked\n"), 1)
			if err := os.MkdirAll(filepath.Join(board, "done"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(original, raw, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(original, filepath.Join(board, "done/TASK-1.md")); err != nil {
				t.Fatal(err)
			}
			req := ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("a", 32), Source: "done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
			_, err = archiveWithStep(board, req, true, false, repairStopAt(point))
			if err == nil || !strings.Contains(err.Error(), "stop at "+point) {
				t.Fatalf("boundary not reached: %v", err)
			}
			before := boardBytes(t, other)
			if _, err := Create(other, CreateRequest{Title: "blocked"}); err == nil || !strings.Contains(err.Error(), "pending archive") {
				t.Fatalf("other board not blocked: %v", err)
			}
			if !reflectEqualBoard(before, boardBytes(t, other)) {
				t.Fatal("blocked writer changed board")
			}
			result, err := RecoverArchive(board, req)
			if err != nil || result.Status != "completed" {
				t.Fatalf("recovery=%+v err=%v", result, err)
			}
			got, err := os.ReadFile(filepath.Join(board, result.Target))
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("archive bytes lost: %v", err)
			}
			if _, err := Create(other, CreateRequest{Title: "unblocked"}); err != nil {
				t.Fatal("common pending not cleared:", err)
			}
		})
	}
}

func TestArchiveRecoveryRechecksPlanChildren(t *testing.T) {
	t.Parallel()
	dir, _, _ := archiveWriterFixture(t)
	entry, err := Create(dir, CreateRequest{Kind: "plan", Title: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: " + entry.Card.ID + "\nchild-ids: [TASK-1]\n---\n")
	if err := os.WriteFile(filepath.Join(dir, entry.Path), raw, 0644); err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{ID: entry.Card.ID, Owner: "worker", RequestID: strings.Repeat("c", 32), Source: entry.Path, ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	_, err = archiveWithStep(dir, req, true, false, repairStopAt("after-archive-journal"))
	if err == nil || !strings.Contains(err.Error(), "stop at") {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "done/TASK-1.md"), filepath.Join(dir, "todo/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := RecoverArchive(dir, req); err == nil || !strings.Contains(err.Error(), "admission denied") {
		t.Fatalf("changed child accepted: %v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("denied recovery changed board")
	}
	if err := os.Rename(filepath.Join(dir, "todo/TASK-1.md"), filepath.Join(dir, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverArchive(dir, req); err != nil {
		t.Fatal("restored child recovery:", err)
	}
}

func TestArchiveRecoveryModeAndAncestorConflicts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"source-mode", "target-mode", "target-ancestor"} {
		t.Run(kind, func(t *testing.T) {
			dir, req, _ := archiveWriterFixture(t)
			_, err := archiveWithStep(dir, req, true, false, repairStopAt("after-target"))
			if err == nil || !strings.Contains(err.Error(), "stop at after-target") {
				t.Fatal("cutpoint not reached:", err)
			}
			target := filepath.Join(dir, "_archive/done/TASK-1.md")
			switch kind {
			case "source-mode":
				if err := os.Chmod(filepath.Join(dir, req.Source), 0640); err != nil {
					t.Fatal(err)
				}
			case "target-mode":
				if err := os.Chmod(target, 0640); err != nil {
					t.Fatal(err)
				}
			case "target-ancestor":
				if err := os.Rename(filepath.Join(dir, "_archive/done"), filepath.Join(dir, "_archive/original")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("original", filepath.Join(dir, "_archive/done")); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, dir)
			_, err = RecoverArchive(dir, req)
			if err == nil {
				t.Fatal("conflicting recovery accepted")
			}
			if kind != "target-ancestor" && !strings.Contains(err.Error(), "mode changed") {
				t.Fatalf("unexpected mode refusal: %v", err)
			}
			if !reflectEqualBoard(before, boardBytes(t, dir)) {
				t.Fatal("conflicting recovery changed board")
			}
			if _, err := os.Lstat(filepath.Join(dir, req.Source)); err != nil {
				t.Fatal("conflicting recovery removed source:", err)
			}
		})
	}
}
