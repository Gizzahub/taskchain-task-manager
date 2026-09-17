package taskstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveRebindArtifactPublishLoadRetryAndInertness(t *testing.T) {
	root := t.TempDir()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	plan := archiveRebindPayloadFixture(t)
	digest, err := publishArchiveRebindArtifact(r, plan)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := archiveRebindArtifactName(digest)
	info, err := r.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("artifact=%v err=%v", info, err)
	}
	loaded, err := loadArchiveRebindArtifact(r, digest)
	if err != nil || !bytes.Equal(loaded.OriginalJournal, plan.OriginalJournal) || !bytes.Equal(loaded.TargetJournal, plan.TargetJournal) {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	firstInfo := info
	if retry, err := publishArchiveRebindArtifact(r, plan); err != nil || retry != digest {
		t.Fatalf("retry digest=%s err=%v", retry, err)
	}
	secondInfo, err := r.Lstat(name)
	if err != nil || !os.SameFile(firstInfo, secondInfo) {
		t.Fatalf("retry replaced artifact: before=%v after=%v err=%v", firstInfo, secondInfo, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("artifact was not inert: entries=%v err=%v", entries, err)
	}
}

func TestArchiveRebindArtifactPublicationCutpoints(t *testing.T) {
	t.Run("stage failure cleans staging", func(t *testing.T) {
		root := t.TempDir()
		r, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		plan := archiveRebindPayloadFixture(t)
		want := errors.New("injected stage failure")
		if _, err := publishArchiveRebindArtifactStep(r, plan, func(point string) error {
			if point == "after-rebind-artifact-stage" {
				return want
			}
			return nil
		}); !errors.Is(err, want) {
			t.Fatalf("stage failure err=%v", err)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("stage failure left files: %v", entries)
		}
	})

	t.Run("link conflict preserves target and no stage", func(t *testing.T) {
		root := t.TempDir()
		r, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		plan := archiveRebindPayloadFixture(t)
		raw, err := archiveRebindPayloadBytes(plan)
		if err != nil {
			t.Fatal(err)
		}
		digest := bytesDigest(raw)
		name, _ := archiveRebindArtifactName(digest)
		conflict := append([]byte(nil), raw...)
		conflict[len(conflict)-1] ^= 1
		var called bool
		if _, err := publishArchiveRebindArtifactStep(r, plan, func(point string) error {
			if point == "after-rebind-artifact-stage" {
				called = true
				if err := os.WriteFile(filepath.Join(root, name), conflict, 0600); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		}); err == nil || !called {
			t.Fatalf("link conflict accepted: called=%v err=%v", called, err)
		}
		after, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, conflict) {
			t.Fatal("link conflict overwrote existing target")
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != name {
			t.Fatalf("link conflict left unexpected files: %v", entries)
		}
	})

	t.Run("link failure keeps artifact and retry is exact", func(t *testing.T) {
		root := t.TempDir()
		r, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		plan := archiveRebindPayloadFixture(t)
		raw, err := archiveRebindPayloadBytes(plan)
		if err != nil {
			t.Fatal(err)
		}
		digest := bytesDigest(raw)
		name, _ := archiveRebindArtifactName(digest)
		if _, err := publishArchiveRebindArtifactStep(r, plan, func(point string) error {
			if point == "after-rebind-artifact-link" {
				return errors.New("injected link publication failure")
			}
			return nil
		}); err == nil {
			t.Fatal("link failure accepted")
		}
		before, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, raw) {
			t.Fatal("published artifact differs from exact payload")
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 1 || entries[0].Name() != name {
			t.Fatalf("post-link failure left unexpected files: %v err=%v", entries, err)
		}
		beforeInfo, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loadArchiveRebindArtifact(r, digest); err != nil {
			t.Fatalf("published artifact not loadable: %v", err)
		}
		if got, err := publishArchiveRebindArtifact(r, plan); err != nil || got != digest {
			t.Fatalf("exact retry digest=%s err=%v", got, err)
		}
		after, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("retry changed artifact: err=%v", err)
		}
		afterInfo, err := os.Stat(filepath.Join(root, name))
		if err != nil || !os.SameFile(beforeInfo, afterInfo) {
			t.Fatalf("retry replaced artifact: err=%v", err)
		}
	})
}

func TestArchiveRebindArtifactRejectsDigestNamesAndAbsentLoad(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, digest := range []string{"", "bad", "../" + strings.Repeat("a", 64), strings.Repeat("A", 64)} {
		if _, err := archiveRebindArtifactName(digest); err == nil {
			t.Fatalf("invalid digest name accepted: %q", digest)
		}
	}
	if _, err := loadArchiveRebindArtifact(r, strings.Repeat("a", 64)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent load err=%v", err)
	}
	entries, err := os.ReadDir(r.Name())
	if err != nil || len(entries) != 0 {
		t.Fatalf("absent load created files: %v err=%v", entries, err)
	}
	plan := archiveRebindPayloadFixture(t)
	payload, err := archiveRebindPayloadBytes(plan)
	if err != nil {
		t.Fatal(err)
	}
	actualDigest := bytesDigest(payload)
	wrongName, _ := archiveRebindArtifactName(strings.Repeat("a", 64))
	if actualDigest == strings.Repeat("a", 64) {
		t.Fatal("wrong fixture digest unexpectedly matched")
	}
	if err := os.WriteFile(filepath.Join(r.Name(), wrongName), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadArchiveRebindArtifact(r, strings.Repeat("a", 64)); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("valid blob under wrong filename err=%v", err)
	}
}

func TestArchiveRebindArtifactRejectsTamperModeLinksAndDirectories(t *testing.T) {
	plan := archiveRebindPayloadFixture(t)
	payload, err := archiveRebindPayloadBytes(plan)
	if err != nil {
		t.Fatal(err)
	}
	digest := bytesDigest(payload)
	name, _ := archiveRebindArtifactName(digest)
	root := t.TempDir()
	write := func(t *testing.T, raw []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), raw, mode); err != nil {
			t.Fatal(err)
		}
	}
	checkLoad := func(t *testing.T, want string) {
		t.Helper()
		r, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		before, err := r.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		var rawBefore []byte
		var linkBefore string
		if before.Mode().IsRegular() {
			rawBefore, err = os.ReadFile(filepath.Join(root, name))
		} else if before.Mode()&os.ModeSymlink != 0 {
			linkBefore, err = os.Readlink(filepath.Join(root, name))
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loadArchiveRebindArtifact(r, digest); err == nil || (want != "" && !strings.Contains(err.Error(), want)) {
			t.Fatalf("tampered artifact err=%v want=%q", err, want)
		}
		if _, err := publishArchiveRebindArtifact(r, plan); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("conflicting artifact publish err=%v want=%q", err, want)
		}
		after, err := r.Lstat(name)
		if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() {
			t.Fatalf("refusal changed existing artifact: %v", err)
		}
		if before.Mode().IsRegular() {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || !bytes.Equal(got, rawBefore) {
				t.Fatalf("refusal changed bytes: %v", err)
			}
		} else if before.Mode()&os.ModeSymlink != 0 {
			got, err := os.Readlink(filepath.Join(root, name))
			if err != nil || got != linkBefore {
				t.Fatalf("refusal changed symlink: %v", err)
			}
			target, err := os.ReadFile(filepath.Join(root, "valid"))
			if err != nil || !bytes.Equal(target, payload) {
				t.Fatalf("refusal changed symlink target: %v", err)
			}
		} else {
			entries, err := os.ReadDir(filepath.Join(root, name))
			if err != nil || len(entries) != 0 {
				t.Fatalf("refusal changed directory: %v", err)
			}
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".task-manager-stage-") {
				t.Fatal("refusal left staging file")
			}
		}
	}
	write(t, append([]byte(nil), payload...), 0644)
	checkLoad(t, "type, mode or size")
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	write(t, payload[:len(payload)-1], 0600)
	checkLoad(t, "digest mismatch")
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "valid"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("valid", filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	checkLoad(t, "type, mode or size")
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
		t.Fatal(err)
	}
	checkLoad(t, "type, mode or size")
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	write(t, payload, 0600)
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, name), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), before...)
	tampered[len(tampered)-1] ^= 1
	if err := os.WriteFile(filepath.Join(root, name), tampered, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := publishArchiveRebindArtifact(r, plan); err == nil {
		t.Fatal("tampered existing artifact overwritten or accepted")
	}
	after, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || !bytes.Equal(tampered, after) {
		// The failed publish must preserve the tampered file, not restore or replace it.
		t.Fatalf("failed publish unexpectedly restored artifact: err=%v", err)
	}
	afterInfo, err := os.Stat(filepath.Join(root, name))
	if err != nil || !os.SameFile(beforeInfo, afterInfo) || afterInfo.Mode().Perm() != 0600 {
		t.Fatalf("failed publish changed artifact identity: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".task-manager-stage-") {
			t.Fatalf("refusal left staging file: %s", entry.Name())
		}
	}
	r.Close()
}

func TestArchiveRebindArtifactRejectsMalformedAndOversizedWithoutStages(t *testing.T) {
	root := t.TempDir()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, raw := range [][]byte{[]byte("bad"), bytes.Repeat([]byte{'x'}, maxArchiveRebindPayloadBytes+1)} {
		digest := bytesDigest(raw)
		name, nameErr := archiveRebindArtifactName(digest)
		if nameErr != nil {
			t.Fatal(nameErr)
		}
		if err := os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadArchiveRebindArtifact(r, digest); err == nil {
			t.Fatal("malformed artifact accepted")
		}
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	framed := make([]byte, archiveRebindPayloadHeader+2)
	copy(framed, archiveRebindPayloadMagic)
	binary.BigEndian.PutUint64(framed[8:16], 1)
	binary.BigEndian.PutUint64(framed[16:24], 1)
	binary.BigEndian.PutUint32(framed[24:28], 0644)
	framed[28], framed[29] = 'x', 'y'
	framedDigest := bytesDigest(framed)
	framedName, err := archiveRebindArtifactName(framedDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, framedName), framed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadArchiveRebindArtifact(r, framedDigest); err == nil || !strings.Contains(err.Error(), "original") {
		t.Fatalf("framed malformed artifact err=%v", err)
	}
	if err := os.Remove(filepath.Join(root, framedName)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("malformed artifact left files: %v err=%v", entries, err)
	}
}
