package taskflow

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotLimitsMatchTheSpec(t *testing.T) {
	if criterionSnapshotEntryLimit != 50_000 || criterionSnapshotBlobLimit != 16<<20 || criterionSnapshotTotalLimit != 256<<20 {
		t.Fatalf("entry/blob/total %d %d %d", criterionSnapshotEntryLimit, criterionSnapshotBlobLimit, criterionSnapshotTotalLimit)
	}
	if criterionTreeListingLimit != 32<<20 || criterionTreeRecordLimit != 64<<10 || criterionSymlinkDepthLimit != 40 {
		t.Fatalf("listing/record/depth %d %d %d", criterionTreeListingLimit, criterionTreeRecordLimit, criterionSymlinkDepthLimit)
	}
	if criterionSnapshotTimeout != 30*time.Second {
		t.Fatal(criterionSnapshotTimeout)
	}
}

func TestSnapshotEntryRecordAndListingLimits(t *testing.T) {
	overRecord := bytes.Repeat([]byte{'a'}, int(criterionTreeRecordLimit)+1)
	if _, err := readSnapshotListing(bytes.NewReader(overRecord)); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("tree entry exceeds %d KiB", criterionTreeRecordLimit>>10)) {
		t.Fatal(err)
	}

	var listing strings.Builder
	for i := 0; i < criterionSnapshotEntryLimit+1; i++ {
		fmt.Fprintf(&listing, "100644 blob %040d\t%06d\x00", i, i)
	}
	if _, err := readSnapshotListing(strings.NewReader(listing.String())); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeds %d entries", criterionSnapshotEntryLimit)) {
		t.Fatal(err)
	}

	if _, err := readSnapshotListing(strings.NewReader("160000 commit abc\tmodules/x\x00")); err == nil || !strings.Contains(err.Error(), "submodule") {
		t.Fatal(err)
	}

	prefix := []byte("100644 blob abcdef\t")
	body := bytes.Repeat([]byte{'p'}, int(criterionTreeRecordLimit)-len(prefix))
	rec := append(append([]byte{}, prefix...), body...)
	rec = append(rec, 0)
	per := int64(len(rec))
	count := int(criterionTreeListingLimit/per) + 1
	_, err := readSnapshotListing(&repeatRecords{left: count, rec: rec})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("tree listing exceeds %d MiB", criterionTreeListingLimit>>20)) {
		t.Fatal(err)
	}
}

type repeatRecords struct {
	left int
	rec  []byte
	off  int
}

func (r *repeatRecords) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.rec[r.off:])
	r.off += n
	if r.off == len(r.rec) {
		r.off = 0
		r.left--
	}
	return n, nil
}

func TestSnapshotBlobLimits(t *testing.T) {
	entries := []snapshotEntry{{mode: "100644", oid: "abc", path: "big"}}
	header := fmt.Sprintf("abc blob %d\n", criterionSnapshotBlobLimit+1)
	err := readSnapshotObjects(context.Background(), strings.NewReader(header), t.TempDir(), entries)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeds %d bytes", criterionSnapshotBlobLimit)) {
		t.Fatal(err)
	}
	if _, err := accountBlob(productionSnapshotBounds(), criterionSnapshotTotalLimit, 1, "x"); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("blobs exceed %d bytes", criterionSnapshotTotalLimit)) {
		t.Fatal(err)
	}
	bounds := snapshotBounds{entries: 4, blob: 16, total: 5}
	pair := []snapshotEntry{{mode: "100644", oid: "a", path: "a.txt"}, {mode: "100644", oid: "b", path: "b.txt"}}
	err = readSnapshotObjectsBounds(context.Background(), bufio.NewReader(strings.NewReader("a blob 3\none\nb blob 3\ntwo\n")), t.TempDir(), pair, bounds)
	if err == nil || !strings.Contains(err.Error(), "exceed 5 bytes") {
		t.Fatal(err)
	}
}

func TestSnapshotLinkGraph(t *testing.T) {
	if err := validateCriterionLinks(linkChain(criterionSymlinkDepthLimit)); err != nil {
		t.Fatal(err)
	}
	if err := validateCriterionLinks(linkChain(criterionSymlinkDepthLimit + 1)); err == nil || !strings.Contains(err.Error(), "symlink chain exceeds") {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		entries []snapshotEntry
		want    string
	}{
		{name: "empty", entries: []snapshotEntry{{mode: "120000", path: "a", link: ""}}, want: "absolute or empty"},
		{name: "absolute", entries: []snapshotEntry{{mode: "120000", path: "a", link: "/etc/passwd"}}, want: "absolute or empty"},
		{name: "cycle", entries: []snapshotEntry{{mode: "120000", path: "a", link: "b"}, {mode: "120000", path: "b", link: "a"}}, want: "symlink cycle"},
		{name: "escape", entries: []snapshotEntry{{mode: "120000", path: "escape", link: "../outside"}}, want: "escapes snapshot"},
		{name: "inside dotdot", entries: []snapshotEntry{{mode: "120000", path: "dir/link", link: "../marker"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCriterionLinks(tc.entries)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func linkChain(n int) []snapshotEntry {
	entries := make([]snapshotEntry, n)
	for i := 0; i < n-1; i++ {
		entries[i] = snapshotEntry{mode: "120000", path: fmt.Sprintf("l%d", i), link: fmt.Sprintf("l%d", i+1)}
	}
	entries[n-1] = snapshotEntry{mode: "120000", path: fmt.Sprintf("l%d", n-1), link: "file"}
	return entries
}

func TestSnapshotPreservesExecuteBit(t *testing.T) {
	dir := t.TempDir()
	if err := writeSnapshotFile(dir, snapshotEntry{mode: "100755", path: "run"}, []byte("#!/bin/sh\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "run"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("mode %o", info.Mode())
	}
}

func TestSnapshotRefusesEscapingSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitForTest(t, root)
	if err := os.WriteFile(filepath.Join(parent, "outside"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	gitAt(t, root, "config", "core.symlinks", "true")
	gitAt(t, root, "add", "-A")
	gitAt(t, root, "commit", "-qm", "link")
	_, err := SelectCriterionTree(context.Background(), root, "HEAD")
	if err == nil || !strings.Contains(err.Error(), "criterion base unavailable") || !strings.Contains(err.Error(), "escapes") {
		t.Fatal(err)
	}
	left, globErr := filepath.Glob(filepath.Join(root, "build", "tmp", "criterion-snapshots", "criterion-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(left) != 0 {
		t.Fatalf("snapshot left behind: %v", left)
	}
}

func TestSnapshotIgnoresReplaceRefs(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	if err := writeFileForTest(root, "marker.txt", []byte("real\n")); err != nil {
		t.Fatal(err)
	}
	gitAt(t, root, "add", "marker.txt")
	gitAt(t, root, "commit", "-qm", "real")
	sha := strings.TrimSpace(gitAt(t, root, "rev-parse", "HEAD"))
	if err := writeFileForTest(root, "marker.txt", []byte("other\n")); err != nil {
		t.Fatal(err)
	}
	gitAt(t, root, "commit", "-am", "other")
	other := strings.TrimSpace(gitAt(t, root, "rev-parse", "HEAD"))
	gitAt(t, root, "replace", sha, other)
	tree, err := SelectCriterionTree(context.Background(), root, sha)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tree.Cleanup(io.Discard) })
	got, err := os.ReadFile(filepath.Join(tree.ProbeDir, "marker.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "real\n" {
		t.Fatalf("snapshot followed the replace ref: %q desc=%s", got, tree.Description)
	}
	if !tree.Selected || strings.Contains(tree.Description, "current tree") || !strings.Contains(tree.Description, sha) {
		t.Fatalf("%#v", tree)
	}
}

func TestSnapshotCancelledContextStops(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := SelectCriterionTree(ctx, root, "HEAD")
	if err == nil || !strings.Contains(err.Error(), "criterion base unavailable") {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancelled context did not stop snapshot creation")
	}
}

func gitAt(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
