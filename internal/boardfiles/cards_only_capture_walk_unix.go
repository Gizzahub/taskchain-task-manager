//go:build darwin || linux

package boardfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

type captureAdd func(SnapshotEntry, fs.FileInfo, captureIdentity) error

// Reopen queued directories from the pinned root, keeping descriptor use
// constant with depth. This trades ancestor reopens for bounded open FDs.
func walkCapture(ctx context.Context, root *os.File, tracked map[string]SnapshotEntry, descendants map[string]bool, add captureAdd) error {
	rootID, err := captureFDIdentity(root)
	if err != nil {
		return err
	}
	identities := map[string]captureIdentity{"": rootID}
	queue := []string{""}
	total := 0
	for cursor := 0; cursor < len(queue); cursor++ {
		prefix := queue[cursor]
		err := visitRecordedCaptureDirectory(ctx, root, prefix, identities, func(dir *os.File) error {
			return listCaptureDirectory(ctx, dir, prefix, tracked, descendants, &total, add, func(rel string, id captureIdentity) {
				identities[rel] = id
				queue = append(queue, rel)
			})
		})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func visitRecordedCaptureDirectory(ctx context.Context, root *os.File, rel string, identities map[string]captureIdentity, visit func(*os.File) error) error {
	dir, err := openRecordedCaptureDirectory(ctx, root, rel, identities)
	if err != nil {
		return err
	}
	err = errors.Join(visit(dir), dir.Close())
	if err != nil {
		return err
	}
	// An opened directory can have been detached before its listing's own
	// before-stat. Revalidate the pathname chain after consuming that FD.
	check, err := openRecordedCaptureDirectory(ctx, root, rel, identities)
	if err != nil {
		return err
	}
	return check.Close()
}

func openRecordedCaptureDirectory(ctx context.Context, root *os.File, rel string, identities map[string]captureIdentity) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := openNoFollowDirectory(int(root.Fd()), ".")
	if err != nil {
		return nil, err
	}
	if err := verifyCaptureFD(current, identities[""]); err != nil {
		return nil, errors.Join(err, current.Close())
	}
	if rel == "" {
		return current, nil
	}
	prefix := ""
	for _, component := range strings.Split(rel, "/") {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, current.Close())
		}
		if prefix != "" {
			prefix += "/"
		}
		prefix += component
		next, err := openNoFollowDirectory(int(current.Fd()), component)
		if err != nil {
			return nil, errors.Join(err, current.Close())
		}
		err = current.Close()
		expected, found := identities[prefix]
		if !found {
			err = errors.Join(err, fmt.Errorf("unobserved capture ancestor: %s", prefix))
		} else {
			err = errors.Join(err, verifyCaptureFD(next, expected))
		}
		if err != nil {
			return nil, errors.Join(err, next.Close())
		}
		current = next
	}
	return current, nil
}

func listCaptureDirectory(ctx context.Context, dir *os.File, prefix string, tracked map[string]SnapshotEntry, descendants map[string]bool, total *int, add captureAdd, enqueue func(string, captureIdentity)) error {
	before, err := captureFDIdentity(dir)
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
			var info fs.FileInfo
			if child.IsDir() {
				if opaqueCaptureDirectory(name) {
					entry.DescendantTracked = descendants[rel]
				} else {
					opened, err := openNoFollowDirectory(int(dir.Fd()), name)
					if err != nil {
						return err
					}
					checkErr := verifyCaptureFD(opened, identity)
					var statErr error
					info, statErr = opened.Stat()
					if err := errors.Join(checkErr, statErr, opened.Close()); err != nil {
						return err
					}
					entry.Mode = info.Mode()
				}
			} else {
				entry.Tracked = entry.Tracked || descendants[rel]
				if entry.Mode.IsRegular() {
					entry.Data, info, err = readCaptureFile(ctx, dir, name, identity)
					if err != nil {
						return fmt.Errorf("read physical file %s: %w", rel, err)
					}
					*total += len(entry.Data)
					if *total > cardsOnlyCaptureTotalLimit {
						return fmt.Errorf("physical board exceeds total byte limit")
					}
					entry.Mode = info.Mode()
				}
			}
			if err := add(entry, info, identity); err != nil {
				return err
			}
			if child.IsDir() && !opaqueCaptureDirectory(name) {
				enqueue(rel, identity)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return verifyCaptureFD(dir, before)
}
