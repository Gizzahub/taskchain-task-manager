package boardfiles

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SnapshotSource describes the caller's selected source, not verified Git
// provenance. None of these values is a policy/adoption wire designation.
type SnapshotSource uint8

const (
	SnapshotWorktree SnapshotSource = iota + 1
	SnapshotIndex
	SnapshotRef
	SnapshotNonGit
)

// SnapshotEntry holds a board-relative path and bytes from the same source.
// Directories may be omitted: their ancestors are inferred. Tracked applies
// to worktree index leaves (including a leaf replaced by a directory).
// Index/ref leaves are tracked and non-Git fails closed.
type SnapshotEntry struct {
	Path    string
	Mode    fs.FileMode
	Tracked bool
	Data    []byte
	// DescendantTracked is caller-supplied Git metadata for an opaque physical
	// directory such as .ce. It never means that this directory itself was an
	// indexed leaf. It is intentionally distinct from indexLeaf.
	DescendantTracked bool
	// Missing is only a tracked worktree regular leaf absent on disk. It
	// blocks projection comparison and is not a card or physical directory.
	Missing   bool
	inferred  bool
	indexLeaf bool
}

// CardsOnlySnapshot owns immutable copies of caller-supplied entries. It does
// not acquire a Git/physical snapshot or prove input completeness/stability.
// In particular it must not authorize filesystem mutation or board adoption.
type CardsOnlySnapshot struct {
	source  SnapshotSource
	entries map[string]SnapshotEntry
	paths   []string
}

func NewCardsOnlySnapshot(source SnapshotSource, entries []SnapshotEntry) (CardsOnlySnapshot, error) {
	return newCardsOnlySnapshot(context.Background(), source, entries)
}

func newCardsOnlySnapshot(ctx context.Context, source SnapshotSource, entries []SnapshotEntry) (CardsOnlySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return CardsOnlySnapshot{}, err
	}
	if source < SnapshotWorktree || source > SnapshotNonGit {
		return CardsOnlySnapshot{}, fmt.Errorf("unknown snapshot source")
	}
	s := CardsOnlySnapshot{source: source, entries: make(map[string]SnapshotEntry)}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return CardsOnlySnapshot{}, err
		}
		if entry.Path == "" || entry.Path == "." || path.IsAbs(entry.Path) ||
			path.Clean(entry.Path) != entry.Path || strings.ContainsAny(entry.Path, "\\\x00") ||
			entry.Path == ".." || strings.HasPrefix(entry.Path, "../") ||
			!utf8.ValidString(entry.Path) || strings.ContainsFunc(entry.Path, unicode.IsControl) {
			return CardsOnlySnapshot{}, fmt.Errorf("unsafe snapshot path %q", entry.Path)
		}
		if _, found := s.entries[entry.Path]; found {
			return CardsOnlySnapshot{}, fmt.Errorf("duplicate snapshot path %q", entry.Path)
		}
		if entry.Mode.IsDir() && len(entry.Data) != 0 {
			return CardsOnlySnapshot{}, fmt.Errorf("directory has snapshot bytes: %q", entry.Path)
		}
		if entry.DescendantTracked && (!entry.Mode.IsDir() || (path.Base(entry.Path) != ".ce" && path.Base(entry.Path) != ".git")) {
			return CardsOnlySnapshot{}, fmt.Errorf("descendant tracking requires opaque metadata directory: %q", entry.Path)
		}
		switch entry.Mode.Type() {
		case 0, fs.ModeDir, fs.ModeSymlink, fs.ModeNamedPipe, fs.ModeSocket, fs.ModeDevice, fs.ModeDevice | fs.ModeCharDevice, fs.ModeIrregular:
		default:
			return CardsOnlySnapshot{}, fmt.Errorf("contradictory snapshot mode for %q", entry.Path)
		}
		if entry.Missing && (source != SnapshotWorktree || !entry.Tracked || !entry.Mode.IsRegular() || len(entry.Data) != 0) {
			return CardsOnlySnapshot{}, fmt.Errorf("invalid missing tracked worktree leaf %q", entry.Path)
		}
		entry.inferred = false
		entry.indexLeaf = source == SnapshotWorktree && entry.Tracked
		entry.Data = bytes.Clone(entry.Data)
		if entry.Mode.IsDir() {
			entry.Tracked = source == SnapshotNonGit || entry.indexLeaf || entry.DescendantTracked
		} else if source != SnapshotWorktree {
			entry.Tracked = true
		}
		s.entries[entry.Path] = entry
	}
	for _, entry := range entries {
		for parent := path.Dir(entry.Path); parent != "."; parent = path.Dir(parent) {
			if err := ctx.Err(); err != nil {
				return CardsOnlySnapshot{}, err
			}
			dir, found := s.entries[parent]
			if found && !dir.Mode.IsDir() {
				return CardsOnlySnapshot{}, fmt.Errorf("non-directory snapshot ancestor %q", parent)
			}
			if found {
				// Explicit directories are validated by their own input entry;
				// inferred directories already have their ancestor chain.
				break
			}
			s.entries[parent] = SnapshotEntry{Path: parent, Mode: fs.ModeDir, inferred: true}
		}
	}
	for rel := range s.entries {
		if err := ctx.Err(); err != nil {
			return CardsOnlySnapshot{}, err
		}
		s.paths = append(s.paths, rel)
	}
	sort.Strings(s.paths)
	// Descendants sort after parents. Propagate once per edge instead of
	// revisiting the whole ancestor chain for every sibling.
	for i := len(s.paths) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return CardsOnlySnapshot{}, err
		}
		rel := s.paths[i]
		entry := s.entries[rel]
		if (source == SnapshotIndex || source == SnapshotRef) && entry.Mode.IsDir() && !entry.Tracked {
			return CardsOnlySnapshot{}, fmt.Errorf("tree snapshot cannot contain empty directory %q", rel)
		}
		if parent := path.Dir(rel); parent != "." {
			dir := s.entries[parent]
			dir.Tracked = dir.Tracked || entry.Tracked || source == SnapshotNonGit
			s.entries[parent] = dir
		}
	}
	return s, nil
}
