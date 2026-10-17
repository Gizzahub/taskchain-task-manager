package boardfiles

import (
	"errors"
	"os"
	"testing"
)

func TestDeclaredReadBoundsAndStableFindings(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.WriteFile("exact.md", []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, issue, err := readDeclaredFile(r, "exact.md", 4); err != nil || issue != "" || string(got) != "1234" {
		t.Fatalf("exact read = %q, %q, %v", got, issue, err)
	}
	if err := r.WriteFile("over.md", []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, issue, err := readDeclaredFile(r, "over.md", 4); err != nil || issue != "declared file exceeds 4 bytes" || len(got) != 0 {
		t.Fatalf("over-limit read = %q, %q, %v", got, issue, err)
	}
	if got, issue, err := readDeclaredFile(r, "missing.md", 4); err != nil || issue != "declared file is missing" || got != nil {
		t.Fatalf("missing read = %q, %q, %v", got, issue, err)
	}
	if err := r.Mkdir("directory.md", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, issue, err := readDeclaredFile(r, "directory.md", 4); err != nil || issue != "declared file is not a regular file" {
		t.Fatalf("directory read = %q, %v", issue, err)
	}
}

func TestDeclaredReadRejectsInvalidPathsAndDetectsReplacement(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, name := range []string{"", ".", "..", "nested/file.md", `nested\\file.md`} {
		if _, _, err := readDeclaredFile(r, name, 1); err == nil {
			t.Errorf("invalid path %q was accepted", name)
		}
	}
	if err := r.WriteFile("declared.md", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = readDeclaredFileWithHook(r, "declared.md", 10, func() error {
		if err := r.Rename("declared.md", "previous.md"); err != nil {
			return err
		}
		return r.WriteFile("declared.md", []byte("new"), 0o600)
	})
	if err == nil || !errors.Is(err, os.ErrNotExist) && err.Error() != "declared file changed while reading" {
		t.Fatalf("replacement error = %v", err)
	}
}

func TestDeclaredReadReturnsErrorForClosedRoot(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, issue, err := readDeclaredFile(r, "declared.md", 1); err == nil || issue != "" {
		t.Fatalf("closed root read = issue %q, error %v", issue, err)
	}
}
