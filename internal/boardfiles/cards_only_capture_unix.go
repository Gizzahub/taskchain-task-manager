//go:build darwin || linux

package boardfiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type physicalObservation struct {
	entries  map[string]SnapshotEntry
	info     map[string]fs.FileInfo
	identity map[string]captureIdentity
}

type captureIdentity struct {
	dev   uint64
	ino   uint64
	mode  uint32
	size  int64
	msec  int64
	mnsec int64
	csec  int64
	cnsec int64
}

func captureCardsOnlyPhysicalPlatform(ctx context.Context, repo, board string, tracked []SnapshotEntry, between func() error) (snapshot CardsOnlySnapshot, err error) {
	if err := validPhysicalBoard(board); err != nil {
		return snapshot, err
	}
	index, err := captureTracked(ctx, tracked)
	if err != nil {
		return snapshot, err
	}
	root, verify, err := openCaptureDirectory(ctx, repo, board)
	if err != nil {
		return snapshot, err
	}
	defer func() {
		err = errors.Join(err, root.Close())
		if err != nil {
			snapshot = CardsOnlySnapshot{}
		}
	}()
	first, err := observeCapture(ctx, root, index, nil)
	if err != nil {
		return snapshot, err
	}
	if between != nil {
		if err := between(); err != nil {
			return snapshot, err
		}
	}
	if _, err := observeCapture(ctx, root, index, &first); err != nil {
		return snapshot, err
	}
	if err := verify(); err != nil {
		return snapshot, err
	}
	entries := make([]SnapshotEntry, 0, len(first.entries))
	for _, entry := range first.entries {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		entries = append(entries, entry)
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot, err = newCardsOnlySnapshot(ctx, SnapshotWorktree, entries)
	return snapshot, errors.Join(err, ctx.Err())
}

func captureTracked(ctx context.Context, entries []SnapshotEntry) (map[string]SnapshotEntry, error) {
	if len(entries) > cardsOnlyCaptureEntryLimit {
		return nil, fmt.Errorf("physical board exceeds entry limit")
	}
	tracked := make(map[string]SnapshotEntry, len(entries))
	pathBytes := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Path == "" || !fs.ValidPath(entry.Path) || entry.Path == "." || strings.Contains(entry.Path, "\\") ||
			!utf8.ValidString(entry.Path) || strings.ContainsFunc(entry.Path, unicode.IsControl) {
			return nil, fmt.Errorf("invalid tracked path %q", entry.Path)
		}
		if entry.Mode.IsDir() || (!entry.Mode.IsRegular() && entry.Mode&fs.ModeSymlink == 0) {
			return nil, fmt.Errorf("unsupported tracked Git mode for %q", entry.Path)
		}
		if _, found := tracked[entry.Path]; found {
			return nil, fmt.Errorf("duplicate tracked path %q", entry.Path)
		}
		pathBytes += len(entry.Path)
		if len(entry.Path) > 64<<10 || pathBytes > cardsOnlyCapturePathLimit {
			return nil, fmt.Errorf("physical board exceeds retained path metadata limit")
		}
		entry.Tracked, entry.Data, entry.Missing, entry.DescendantTracked = true, nil, false, false
		tracked[entry.Path] = entry
	}
	if err := newCapturePathBudget(ctx, tracked).err; err != nil {
		return nil, err
	}
	return tracked, nil
}

type capturePathBudget struct {
	paths map[string]struct{}
	bytes int
	err   error
}

func newCapturePathBudget(ctx context.Context, tracked map[string]SnapshotEntry) capturePathBudget {
	b := capturePathBudget{paths: make(map[string]struct{})}
	for rel := range tracked {
		for parent := rel; parent != "."; parent = path.Dir(parent) {
			if err := ctx.Err(); err != nil {
				b.err = err
				return b
			}
			if _, found := b.paths[parent]; found {
				break
			}
			b.add(parent)
			if b.err != nil {
				return b
			}
		}
	}
	return b
}

func (b *capturePathBudget) add(rel string) {
	if b.err != nil {
		return
	}
	if _, found := b.paths[rel]; found {
		return
	}
	b.paths[rel] = struct{}{}
	b.bytes += len(rel)
	if len(b.paths) > cardsOnlyCaptureEntryLimit || b.bytes > cardsOnlyCapturePathLimit {
		b.err = fmt.Errorf("physical board exceeds retained path metadata limit")
	}
}

func openCaptureDirectory(ctx context.Context, repo, board string) (*os.File, func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	repoPath, err := filepath.Abs(repo)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve repository path: %w", err)
	}
	components := append(strings.FieldsFunc(strings.TrimPrefix(repoPath, string(os.PathSeparator)), func(r rune) bool {
		return r == os.PathSeparator
	}), strings.Split(board, "/")...)
	root, err := openNoFollowDirectory(unix.AT_FDCWD, string(os.PathSeparator))
	if err != nil {
		return nil, nil, fmt.Errorf("open repository without following links: %w", err)
	}
	infos := []fs.FileInfo{}
	rootInfo, err := root.Stat()
	if err != nil {
		return nil, nil, errors.Join(err, root.Close())
	}
	infos = append(infos, rootInfo)
	current := root
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return nil, nil, errors.Join(err, current.Close())
		}
		next, err := openNoFollowDirectory(int(current.Fd()), component)
		if err != nil {
			return nil, nil, errors.Join(fmt.Errorf("open board component without following links: %w", err), current.Close())
		}
		if closeErr := current.Close(); closeErr != nil {
			return nil, nil, errors.Join(closeErr, next.Close())
		}
		current = next
		info, err := current.Stat()
		if err != nil {
			return nil, nil, errors.Join(err, current.Close())
		}
		infos = append(infos, info)
	}
	verify := func() (err error) {
		check, err := openNoFollowDirectory(unix.AT_FDCWD, string(os.PathSeparator))
		if err != nil {
			return fmt.Errorf("repository changed during capture: %w", err)
		}
		defer func() { err = errors.Join(err, check.Close()) }()
		for i, component := range components {
			if err := ctx.Err(); err != nil {
				return err
			}
			next, openErr := openNoFollowDirectory(int(check.Fd()), component)
			if openErr != nil {
				return fmt.Errorf("board component changed during capture: %w", openErr)
			}
			info, statErr := next.Stat()
			if statErr != nil || !os.SameFile(infos[i+1], info) {
				return errors.Join(fmt.Errorf("capture directory changed: %s", component), statErr, next.Close())
			}
			if closeErr := check.Close(); closeErr != nil {
				next.Close()
				return closeErr
			}
			check = next
		}
		return nil
	}
	return current, verify, nil
}

func openNoFollowDirectory(parentFD int, name string) (*os.File, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, statErr := file.Stat()
	if statErr != nil || !info.IsDir() {
		return nil, errors.Join(statErr, file.Close(), fmt.Errorf("not a directory"))
	}
	return file, nil
}

func observeCapture(ctx context.Context, root *os.File, tracked map[string]SnapshotEntry, expected *physicalObservation) (physicalObservation, error) {
	o := physicalObservation{entries: make(map[string]SnapshotEntry), info: make(map[string]fs.FileInfo), identity: make(map[string]captureIdentity)}
	budget := newCapturePathBudget(ctx, tracked)
	if budget.err != nil {
		return o, budget.err
	}
	descendants, err := trackedDescendants(ctx, tracked)
	if err != nil {
		return o, err
	}
	add := func(entry SnapshotEntry, info fs.FileInfo, identity captureIdentity) error {
		budget.add(entry.Path)
		if budget.err != nil {
			return budget.err
		}
		if len(o.entries) >= cardsOnlyCaptureEntryLimit {
			return fmt.Errorf("physical board exceeds entry limit")
		}
		if old, found := o.entries[entry.Path]; found {
			return fmt.Errorf("duplicate physical board path %q (%v)", entry.Path, old.Mode)
		}
		if expected != nil {
			prior, found := expected.entries[entry.Path]
			if !found || prior.Mode != entry.Mode || prior.Tracked != entry.Tracked || prior.Missing != entry.Missing || prior.DescendantTracked != entry.DescendantTracked || !bytes.Equal(prior.Data, entry.Data) {
				return fmt.Errorf("board changed between physical observations: %s", entry.Path)
			}
			if priorInfo, found := expected.info[entry.Path]; found != (info != nil) || (info != nil && (!os.SameFile(priorInfo, info) || !sameDeclaredState(priorInfo, info))) {
				return fmt.Errorf("physical entry changed between observations: %s", entry.Path)
			}
			if priorIdentity, found := expected.identity[entry.Path]; !found || priorIdentity != identity {
				return fmt.Errorf("physical entry changed between observations: %s", entry.Path)
			}
		}
		stored := entry
		if expected != nil {
			stored.Data = nil
		}
		o.entries[entry.Path] = stored
		if info != nil {
			o.info[entry.Path] = info
		}
		o.identity[entry.Path] = identity
		return nil
	}
	if err := walkCapture(ctx, root, tracked, descendants, add); err != nil {
		return o, err
	}
	for rel, indexed := range tracked {
		if err := ctx.Err(); err != nil {
			return o, err
		}
		if _, found := o.entries[rel]; found || underOpaqueOrNonDirectory(o.entries, rel) {
			continue
		}
		if indexed.Mode&fs.ModeSymlink != 0 {
			return o, fmt.Errorf("missing tracked symlink cannot be captured safely: %s", rel)
		}
		if err := add(SnapshotEntry{Path: rel, Mode: indexed.Mode, Tracked: true, Missing: true}, nil, captureIdentity{}); err != nil {
			return o, err
		}
	}
	if expected != nil && len(expected.entries) != len(o.entries) {
		return o, fmt.Errorf("board changed between physical observations")
	}
	return o, nil
}

func opaqueCaptureDirectory(name string) bool { return name == ".ce" || name == ".git" }

func trackedDescendants(ctx context.Context, tracked map[string]SnapshotEntry) (map[string]bool, error) {
	descendants := make(map[string]bool)
	for rel := range tracked {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if descendants[parent] {
				break
			}
			descendants[parent] = true
		}
	}
	return descendants, nil
}

func underOpaqueOrNonDirectory(entries map[string]SnapshotEntry, rel string) bool {
	for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
		if entry, found := entries[parent]; found && (!entry.Mode.IsDir() || opaqueCaptureDirectory(path.Base(parent))) {
			return true
		}
	}
	return false
}

func captureEntryIdentity(parentFD int, name string) (captureIdentity, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return captureIdentity{}, err
	}
	return captureStatIdentity(stat), nil
}
