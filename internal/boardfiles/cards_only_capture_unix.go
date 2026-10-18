//go:build darwin || linux

package boardfiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	index, err := captureTracked(tracked)
	if err != nil {
		return snapshot, err
	}
	root, verify, err := openCaptureDirectory(repo, board)
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
		entries = append(entries, entry)
	}
	return NewCardsOnlySnapshot(SnapshotWorktree, entries)
}

func captureTracked(entries []SnapshotEntry) (map[string]SnapshotEntry, error) {
	if len(entries) > cardsOnlyCaptureEntryLimit {
		return nil, fmt.Errorf("physical board exceeds entry limit")
	}
	tracked := make(map[string]SnapshotEntry, len(entries))
	pathBytes := 0
	for _, entry := range entries {
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
	if err := newCapturePathBudget(tracked).err; err != nil {
		return nil, err
	}
	return tracked, nil
}

type capturePathBudget struct {
	paths map[string]struct{}
	bytes int
	err   error
}

func newCapturePathBudget(tracked map[string]SnapshotEntry) capturePathBudget {
	b := capturePathBudget{paths: make(map[string]struct{})}
	for rel := range tracked {
		for parent := rel; parent != "."; parent = path.Dir(parent) {
			b.add(parent)
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

func openCaptureDirectory(repo, board string) (*os.File, func() error, error) {
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
	budget := newCapturePathBudget(tracked)
	if budget.err != nil {
		return o, budget.err
	}
	descendants := trackedDescendants(tracked)
	total := 0
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
	var walk func(*os.File, string) error
	walk = func(dir *os.File, prefix string) error {
		if _, err := dir.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind physical directory: %w", err)
		}
		before, err := dir.Stat()
		if err != nil {
			return err
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			batch, readErr := dir.ReadDir(256)
			for _, child := range batch {
				if err := ctx.Err(); err != nil {
					return err
				}
				name := child.Name()
				rel := name
				if prefix != "" {
					rel = prefix + "/" + name
				}
				if len(rel) > 64<<10 || !fs.ValidPath(rel) || strings.Contains(rel, "\\") ||
					!utf8.ValidString(rel) || strings.ContainsFunc(rel, unicode.IsControl) {
					return fmt.Errorf("unsafe physical board path %q", rel)
				}
				identity, err := captureEntryIdentity(int(dir.Fd()), name)
				if err != nil {
					return fmt.Errorf("stat physical entry %s: %w", rel, err)
				}
				_, exactTracked := tracked[rel]
				entry := SnapshotEntry{Path: rel, Mode: child.Type(), Tracked: exactTracked}
				if child.IsDir() {
					opaque := opaqueCaptureDirectory(name)
					entry.DescendantTracked = opaque && descendants[rel]
					if opaque {
						if err := add(entry, nil, identity); err != nil {
							return err
						}
						continue
					}
					childDir, err := openNoFollowDirectory(int(dir.Fd()), name)
					if err != nil {
						return fmt.Errorf("open physical directory without following links: %s: %w", rel, err)
					}
					opened, statErr := childDir.Stat()
					if statErr != nil {
						return errors.Join(fmt.Errorf("stat physical directory: %s", rel), statErr, childDir.Close())
					}
					entry.Mode = opened.Mode()
					if err := add(entry, opened, identity); err != nil {
						childDir.Close()
						return err
					}
					err = walk(childDir, rel)
					err = errors.Join(err, childDir.Close())
					if err != nil {
						return err
					}
				} else {
					entry.Tracked = entry.Tracked || descendants[rel]
					if entry.Mode.IsRegular() {
						data, info, err := readCaptureFile(dir, name, identity)
						if err != nil {
							return fmt.Errorf("read physical file %s: %w", rel, err)
						}
						total += len(data)
						if total > cardsOnlyCaptureTotalLimit {
							return fmt.Errorf("physical board exceeds total byte limit")
						}
						entry.Data = data
						entry.Mode = info.Mode()
						if err := add(entry, info, identity); err != nil {
							return err
						}
						continue
					}
					if err := add(entry, nil, identity); err != nil {
						return err
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		after, err := dir.Stat()
		if err != nil || !os.SameFile(before, after) || !sameDeclaredState(before, after) {
			return errors.Join(fmt.Errorf("physical directory changed during listing"), err)
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return o, err
	}
	for rel, indexed := range tracked {
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

func trackedDescendants(tracked map[string]SnapshotEntry) map[string]bool {
	descendants := make(map[string]bool)
	for rel := range tracked {
		for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
			descendants[parent] = true
		}
	}
	return descendants
}

func underOpaqueOrNonDirectory(entries map[string]SnapshotEntry, rel string) bool {
	for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
		if entry, found := entries[parent]; found && (!entry.Mode.IsDir() || opaqueCaptureDirectory(path.Base(parent))) {
			return true
		}
	}
	return false
}

func readCaptureFile(dir *os.File, name string, before captureIdentity) ([]byte, fs.FileInfo, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return nil, nil, errors.Join(err, file.Close(), fmt.Errorf("file is not regular"))
	}
	data, readErr := io.ReadAll(io.LimitReader(file, cardsOnlyCaptureFileLimit+1))
	after, statErr := file.Stat()
	afterIdentity, identityErr := captureEntryIdentity(int(dir.Fd()), name)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, identityErr, closeErr); err != nil {
		return nil, nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(opened, after) || !sameDeclaredState(opened, after) || before != afterIdentity {
		return nil, nil, fmt.Errorf("file changed while reading")
	}
	if len(data) > cardsOnlyCaptureFileLimit {
		return nil, nil, fmt.Errorf("file exceeds %d bytes", cardsOnlyCaptureFileLimit)
	}
	return data, opened, nil
}

func captureEntryIdentity(parentFD int, name string) (captureIdentity, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return captureIdentity{}, err
	}
	return captureIdentity{
		dev: uint64(stat.Dev), ino: uint64(stat.Ino), mode: uint32(stat.Mode), size: stat.Size,
		msec: stat.Mtim.Sec, mnsec: stat.Mtim.Nsec, csec: stat.Ctim.Sec, cnsec: stat.Ctim.Nsec,
	}, nil
}
