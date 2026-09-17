package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func emptyArchiveJournal() archiveJournal {
	return archiveJournal{SchemaVersion: 1, BoardPath: "/synthetic/tasks", Records: []archiveRecord{}}
}

func openArchiveTestRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

func assertNoArchiveStageFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".task-manager-stage-") {
			t.Fatalf("staging file left behind: %s", entry.Name())
		}
	}
}

func TestArchiveJournalIOValidRoundTripAndUpdate(t *testing.T) {
	r, dir := openArchiveTestRoot(t)
	j, _, _, _ := archiveJournalFixture(t)
	if err := saveArchiveJournal(r, j, true); err != nil {
		t.Fatal("initial save:", err)
	}
	got, err := loadArchiveJournal(r)
	if err != nil || !reflect.DeepEqual(got, j) {
		t.Fatalf("round trip=%+v err=%v", got, err)
	}
	assertNoArchiveStageFiles(t, dir)

	updated := emptyArchiveJournal()
	if err := saveArchiveJournal(r, updated, false); err != nil {
		t.Fatal("existing update:", err)
	}
	got, err = loadArchiveJournal(r)
	if err != nil || !reflect.DeepEqual(got, updated) {
		t.Fatalf("updated=%+v err=%v", got, err)
	}
	assertNoArchiveStageFiles(t, dir)
}

func TestArchiveJournalIOInitialSaveNeverOverwrites(t *testing.T) {
	r, dir := openArchiveTestRoot(t)
	first := emptyArchiveJournal()
	if err := saveArchiveJournal(r, first, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, archivesFile))
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Namespace = strings.Repeat("a", 32)
	if err := saveArchiveJournal(r, second, true); err == nil {
		t.Fatal("initial save overwrote existing journal")
	}
	after, err := os.ReadFile(filepath.Join(dir, archivesFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing journal changed: %v", err)
	}
	assertNoArchiveStageFiles(t, dir)
}

func TestArchiveJournalIOMissingUpdateAndMalformedRead(t *testing.T) {
	r, dir := openArchiveTestRoot(t)
	if err := saveArchiveJournal(r, emptyArchiveJournal(), false); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing update err=%v", err)
	}
	assertNoArchiveStageFiles(t, dir)
	if err := os.WriteFile(filepath.Join(dir, archivesFile), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadArchiveJournal(r); err == nil {
		t.Fatal("malformed journal loaded")
	}
}

func TestArchiveJournalIOSymlinkAndDirectoryRejected(t *testing.T) {
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			r, dir := openArchiveTestRoot(t)
			valid, err := archiveJournalBytes(emptyArchiveJournal())
			if err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.WriteFile(filepath.Join(dir, "elsewhere"), valid, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(dir, archivesFile)); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(filepath.Join(dir, archivesFile), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := loadArchiveJournal(r); err == nil {
				t.Fatal("non-regular journal loaded")
			}
			if err := saveArchiveJournal(r, emptyArchiveJournal(), false); err == nil {
				t.Fatal("non-regular journal updated")
			}
			if err := saveArchiveJournal(r, emptyArchiveJournal(), true); err == nil {
				t.Fatal("initial publication replaced non-regular journal")
			}
			if kind == "symlink" {
				after, err := os.ReadFile(filepath.Join(dir, "elsewhere"))
				if err != nil || !bytes.Equal(valid, after) {
					t.Fatalf("symlink target changed: %v", err)
				}
			}
			assertNoArchiveStageFiles(t, dir)
		})
	}
}
