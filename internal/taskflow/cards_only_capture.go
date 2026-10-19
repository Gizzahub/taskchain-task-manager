package taskflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
)

// CardsOnlyCapture is an inactive reader result, never mutation/adoption evidence.
// Ref uses an immutable tree. Index/worktree provide matching observations, not
// an atomic snapshot or protection against arbitrary concurrent writers.
type CardsOnlyCapture struct {
	Snapshot    boardfiles.CardsOnlySnapshot
	TreeOID     string
	IndexDigest string
}

type captureLeaf struct{ name, mode, oid string }

// CaptureCardsOnlyRef reads paths and bytes from one resolved Git tree.
func CaptureCardsOnlyRef(ctx context.Context, root, board, ref string) (CardsOnlyCapture, error) {
	binding, err := newCaptureBinding(ctx, root)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	root = binding.root
	if err := captureBoardPath(board); err != nil {
		return CardsOnlyCapture{}, err
	}
	tree, err := captureGit(ctx, root, 256, "rev-parse", "--verify", "--end-of-options", ref+"^{tree}")
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	oid := strings.TrimSpace(string(tree))
	if !captureOID(oid) {
		return CardsOnlyCapture{}, fmt.Errorf("invalid resolved tree OID")
	}
	raw, err := captureGit(ctx, root, 16<<20, "ls-tree", "-r", "-z", "--full-tree", oid, "--", board)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	leaves, err := parseCaptureListing(raw, board, false)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	entries, err := captureBlobs(ctx, root, leaves)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	snapshot, err := boardfiles.NewCardsOnlySnapshot(boardfiles.SnapshotRef, entries)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	if err := binding.verify(ctx, false); err != nil {
		return CardsOnlyCapture{}, err
	}
	return CardsOnlyCapture{Snapshot: snapshot, TreeOID: oid}, err
}

// CaptureCardsOnlyIndex reads stage-zero OIDs and rechecks the board listing.
func CaptureCardsOnlyIndex(ctx context.Context, root, board string) (CardsOnlyCapture, error) {
	return captureCardsOnlyIndex(ctx, root, board, nil)
}
func captureCardsOnlyIndex(ctx context.Context, root, board string, between func() error) (CardsOnlyCapture, error) {
	binding, err := newCaptureBinding(ctx, root)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	root = binding.root
	leaves, before, err := captureIndex(ctx, root, board)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	entries, err := captureBlobs(ctx, root, leaves)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	if between != nil {
		if err := between(); err != nil {
			return CardsOnlyCapture{}, err
		}
	}
	_, after, err := captureIndex(ctx, root, board)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	if !bytes.Equal(before, after) {
		return CardsOnlyCapture{}, fmt.Errorf("board index changed during capture")
	}
	snapshot, err := boardfiles.NewCardsOnlySnapshot(boardfiles.SnapshotIndex, entries)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	if err := binding.verify(ctx, true); err != nil {
		return CardsOnlyCapture{}, err
	}
	return CardsOnlyCapture{Snapshot: snapshot, IndexDigest: fmt.Sprintf("%x", sha256.Sum256(before))}, err
}

// CaptureCardsOnlyWorktree binds a pinned physical observation to the index
// leaves observed before and after it. Ignored files are deliberately included.
func CaptureCardsOnlyWorktree(ctx context.Context, root, board string) (CardsOnlyCapture, error) {
	binding, err := newCaptureBinding(ctx, root)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	root = binding.root
	leaves, before, err := captureIndex(ctx, root, board)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	tracked := make([]boardfiles.SnapshotEntry, len(leaves))
	for i, leaf := range leaves {
		tracked[i] = boardfiles.SnapshotEntry{Path: leaf.name, Mode: captureMode(leaf.mode), Tracked: true}
	}
	snapshot, err := boardfiles.CaptureCardsOnlyPhysical(ctx, root, board, tracked)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	_, after, err := captureIndex(ctx, root, board)
	if err != nil {
		return CardsOnlyCapture{}, err
	}
	if !bytes.Equal(before, after) {
		return CardsOnlyCapture{}, fmt.Errorf("board index changed during worktree capture")
	}
	if err := binding.verify(ctx, true); err != nil {
		return CardsOnlyCapture{}, err
	}
	return CardsOnlyCapture{Snapshot: snapshot, IndexDigest: fmt.Sprintf("%x", sha256.Sum256(before))}, nil
}

func captureIndex(ctx context.Context, root, board string) ([]captureLeaf, []byte, error) {
	if err := captureBoardPath(board); err != nil {
		return nil, nil, err
	}
	// Intent-to-add is not a real index blob even when ls-files prints the
	// empty-blob OID. Compare Git's documented visibility modes explicitly.
	var views [2][]byte
	for i, flag := range []string{"--ita-visible-in-index", "--ita-invisible-in-index"} {
		data, err := captureGit(ctx, root, 16<<20, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--name-only", "-z", flag, "--", board)
		if err != nil {
			return nil, nil, err
		}
		views[i] = data
	}
	if !bytes.Equal(views[0], views[1]) {
		return nil, nil, fmt.Errorf("board index contains intent-to-add entries")
	}
	flags, err := captureGit(ctx, root, 16<<20, "ls-files", "-v", "-z", "--", board)
	if err != nil {
		return nil, nil, err
	}
	flagPaths := map[string]bool{}
	for remaining := flags; len(remaining) > 0; {
		record, rest, ok := bytes.Cut(remaining, []byte{0})
		if !ok {
			return nil, nil, fmt.Errorf("unterminated index flags record")
		}
		remaining = rest
		if len(record) < 3 || record[1] != ' ' {
			return nil, nil, fmt.Errorf("invalid index flags record")
		}
		if len(record) > (64<<10)+2 {
			return nil, nil, fmt.Errorf("index flags path exceeds byte limit")
		}
		if record[0] == 'S' || record[0] == 's' {
			return nil, nil, fmt.Errorf("skip-worktree board entries are unavailable")
		}
		if !strings.ContainsRune("HhMmRrCcKk?", rune(record[0])) {
			return nil, nil, fmt.Errorf("unsupported index flags")
		}
		name := string(record[2:])
		if flagPaths[name] {
			return nil, nil, fmt.Errorf("duplicate index flags path")
		}
		flagPaths[name] = true
		if len(flagPaths) > 100000 {
			return nil, nil, fmt.Errorf("index flags exceed entry limit")
		}
	}
	raw, err := captureGit(ctx, root, 16<<20, "ls-files", "--stage", "-z", "--", board)
	if err != nil {
		return nil, nil, err
	}
	leaves, err := parseCaptureListing(raw, board, true)
	if err != nil {
		return nil, nil, err
	}
	if len(leaves) != len(flagPaths) {
		return nil, nil, fmt.Errorf("board index flags and paths changed during listing")
	}
	for _, leaf := range leaves {
		if !flagPaths[board+"/"+leaf.name] {
			return nil, nil, fmt.Errorf("board index paths changed during listing")
		}
	}
	// Flags are included so their observed changes cannot silently reuse the
	// same stage/mode/OID/path digest.
	return leaves, append(raw, flags...), err
}

func parseCaptureListing(raw []byte, board string, index bool) ([]captureLeaf, error) {
	if len(raw) > 0 && raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("unterminated board Git listing")
	}
	leaves := []captureLeaf{}
	seen := map[string]bool{}
	allPaths := map[string]bool{}
	metadataBytes := 0
	for len(raw) > 0 {
		record, rest, ok := bytes.Cut(raw, []byte{0})
		if !ok || len(record) == 0 {
			return nil, fmt.Errorf("invalid board Git record boundary")
		}
		if len(record) > (64<<10)+256 {
			return nil, fmt.Errorf("board Git record exceeds byte limit")
		}
		raw = rest
		meta, name, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("invalid board Git record")
		}
		if index && fields[2] != "0" {
			return nil, fmt.Errorf("unmerged board index entry %q", name)
		}
		mode, oid := fields[0], fields[2]
		if index {
			oid = fields[1]
		} else if fields[1] != "blob" {
			return nil, fmt.Errorf("unsupported board Git object %q", name)
		}
		if mode != "100644" && mode != "100755" && mode != "120000" {
			return nil, fmt.Errorf("unsupported board Git mode %s", mode)
		}
		if !captureOID(oid) {
			return nil, fmt.Errorf("invalid board blob OID")
		}
		if !strings.HasPrefix(string(name), board+"/") {
			return nil, fmt.Errorf("board path is not a Git directory: %q", name)
		}
		rel := strings.TrimPrefix(string(name), board+"/")
		if err := captureRelativePath(rel); err != nil {
			return nil, err
		}
		if seen[rel] {
			return nil, fmt.Errorf("duplicate board Git path %q", rel)
		}
		seen[rel] = true
		for part := rel; part != "."; part = path.Dir(part) {
			if !allPaths[part] {
				if len(part) > (16<<20)-metadataBytes {
					return nil, fmt.Errorf("board capture metadata exceeds path byte limit")
				}
				metadataBytes += len(part)
			}
			allPaths[part] = true
			if len(allPaths) > 100000 {
				return nil, fmt.Errorf("board capture exceeds inferred entry limit")
			}
		}
		leaves = append(leaves, captureLeaf{rel, mode, oid})
		if len(leaves) > 100000 {
			return nil, fmt.Errorf("board capture exceeds entry limit")
		}
	}
	return leaves, nil
}

func captureBlobs(ctx context.Context, root string, leaves []captureLeaf) ([]boardfiles.SnapshotEntry, error) {
	entries := make([]boardfiles.SnapshotEntry, 0, len(leaves))
	folded := map[string]bool{}
	total := 0
	for _, leaf := range leaves {
		kind, err := captureGit(ctx, root, 32, "cat-file", "-t", leaf.oid)
		if err != nil {
			return nil, err
		}
		if string(kind) != "blob\n" {
			return nil, fmt.Errorf("board leaf does not name a blob")
		}
		// Legacy .ce has one structural finding; never read its blob contents.
		parts := strings.Split(leaf.name, "/")
		fold := ""
		for i, p := range parts {
			if (p == ".ce" || p == ".git") && i < len(parts)-1 {
				fold = strings.Join(parts[:i+1], "/")
				break
			}
		}
		if fold != "" {
			if !folded[fold] {
				entries = append(entries, boardfiles.SnapshotEntry{Path: fold, Mode: fs.ModeDir, DescendantTracked: true})
				folded[fold] = true
			}
			continue
		}
		sizeText, err := captureGit(ctx, root, 32, "cat-file", "-s", leaf.oid)
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(string(sizeText)))
		if err != nil || size < 0 || size > 1<<20 || size > (64<<20)-total {
			return nil, fmt.Errorf("board blob exceeds capture byte limits")
		}
		data, err := captureGit(ctx, root, size, "cat-file", "blob", leaf.oid)
		if err != nil {
			return nil, err
		}
		if len(data) != size {
			return nil, fmt.Errorf("board blob size changed")
		}
		total += len(data)
		if total > 64<<20 {
			return nil, fmt.Errorf("board capture exceeds total byte limit")
		}
		mode := captureMode(leaf.mode)
		entries = append(entries, boardfiles.SnapshotEntry{Path: leaf.name, Mode: mode, Tracked: true, Data: data})
	}
	return entries, nil
}

func captureBoardPath(name string) error {
	return boardfiles.ValidateBoardPath(name)
}

func captureRelativePath(name string) error {
	return boardfiles.ValidateCaptureRelativePath(name)
}
func captureMode(mode string) fs.FileMode {
	if mode == "120000" {
		return fs.ModeSymlink
	}
	if mode == "100755" {
		return 0755
	}
	return 0644
}

func captureOID(oid string) bool {
	if len(oid) != 40 && len(oid) != 64 {
		return false
	}
	for _, c := range oid {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return strings.Trim(oid, "0") != ""
}

type captureOutput struct {
	data   bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

// Do not embed bytes.Buffer: its promoted ReadFrom would bypass Write limits
// when os/exec copies a subprocess pipe with io.Copy.
func (b *captureOutput) Len() int       { return b.data.Len() }
func (b *captureOutput) Bytes() []byte  { return b.data.Bytes() }
func (b *captureOutput) String() string { return b.data.String() }

func (b *captureOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.cancel()
		return 0, fmt.Errorf("board capture Git output exceeds %d bytes", b.limit)
	}
	return b.data.Write(p)
}
