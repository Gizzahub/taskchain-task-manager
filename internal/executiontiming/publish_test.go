package executiontiming

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPublishIsDeterministicAndIdempotent(t *testing.T) {
	dir := privateDir(t)
	r := validRecord()
	first, err := Publish(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Publish(dir, r)
	if err != nil || first != second {
		t.Fatalf("second %q: %v", second, err)
	}
	info, err := os.Stat(first)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private artifact: %v, %#o", err, info.Mode().Perm())
	}
	r.Status = StatusFailure
	if _, err := Publish(dir, r); err == nil {
		t.Fatal("different bytes at same run ID unexpectedly passed")
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(first), ".execution-timing-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary artifacts retained: %v %v", leftovers, err)
	}
	if filepath.Base(first) == r.RunID+".json" {
		t.Fatal("raw run id appears in filename")
	}
}

func TestPublishCreatesMissingLedgerAndKeepsLegacyPermissions(t *testing.T) {
	base := privateDir(t)
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(base, "ce", "task-runtime", "v1")
	if _, err := Publish(ledger, validRecord()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(base)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("legacy ledger permissions changed: %v %#o", err, info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Join(ledger, "execution-timing"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("new directory permissions: %v %#o", err, info.Mode().Perm())
	}
}

func TestPublishConcurrentAndRefusesSymlinks(t *testing.T) {
	dir := privateDir(t)
	r := validRecord()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := Publish(dir, r); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent publish: %v", err)
		}
	}
	badRoot := filepath.Join(t.TempDir(), "ledger-link")
	if err := os.Symlink(dir, badRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(badRoot, validRecord()); err == nil {
		t.Fatal("symlink root accepted")
	}
	badDir := privateDir(t)
	if err := os.Symlink(dir, filepath.Join(badDir, "execution-timing")); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(badDir, validRecord()); err == nil {
		t.Fatal("symlink parent accepted")
	}
}
