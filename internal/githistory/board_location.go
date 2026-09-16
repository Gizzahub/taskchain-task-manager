package githistory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type BoardLocation struct {
	Repository      string
	CommonDirectory string
	Board           string
	NamespaceKey    string
}

// LocateBoard returns nil only when no containing .git boundary exists. A bad
// boundary is an error, never permission to fall back to independent allocation.
func LocateBoard(ctx context.Context, dir string) (*BoardLocation, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, errors.New("empty board path")
	}
	// Resolve existing ancestors before looking for .git. Otherwise an external
	// symlink into a repository subdirectory could appear to be standalone.
	existing := abs
	missing := []string{}
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, filepath.Base(existing))
		parent := filepath.Dir(existing)
		if parent == existing {
			return nil, errors.New("cannot resolve board ancestors")
		}
		existing = parent
	}
	abs, err = filepath.EvalSymlinks(existing)
	if err != nil {
		return nil, err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		abs = filepath.Join(abs, missing[i])
	}
	current := abs
	for {
		_, err := os.Lstat(filepath.Join(current, ".git"))
		if err == nil {
			rel, err := filepath.Rel(current, abs)
			if err != nil {
				return nil, err
			}
			board := filepath.ToSlash(rel)
			s, err := newScanner(current, board)
			if err != nil {
				return nil, err
			}
			if err := s.safe(ctx); err != nil {
				return nil, err
			}
			common, _, err := s.commonDirectory(ctx)
			if err != nil {
				return nil, err
			}
			if err := s.validateBoardParents(); err != nil {
				return nil, err
			}
			return &BoardLocation{Repository: s.root, CommonDirectory: common, Board: board, NamespaceKey: fmt.Sprintf("%x", sha256.Sum256([]byte(board)))}, nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrInvalid) {
			return nil, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil, nil
		}
		current = parent
	}
}

// Missing trailing components are allowed for init; existing ones must have
// exact names and no symlinks. This also rejects case aliases before creation.
func (s scanner) validateBoardParents() error {
	current := s.root
	for _, part := range strings.Split(s.board, "/") {
		if strings.EqualFold(part, ".git") {
			return errors.New("board cannot be Git metadata")
		}
		entries, err := os.ReadDir(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		found := false
		for _, entry := range entries {
			if entry.Name() == part {
				found = true
			}
			if strings.EqualFold(entry.Name(), part) && entry.Name() != part {
				return errors.New("board path case alias is ambiguous")
			}
		}
		if !found {
			return nil
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("board path requires real directories")
		}
	}
	return nil
}
