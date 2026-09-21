package githistory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

type WorktreeReport struct {
	SchemaVersion   int                         `json:"schemaVersion"`
	Repository      string                      `json:"repository"`
	CommonDirectory string                      `json:"commonDirectory"`
	Board           string                      `json:"board"`
	NamespaceKey    string                      `json:"namespaceKey"`
	Worktrees       []Worktree                  `json:"worktrees"`
	SharedReadiness outputvocab.ValidationState `json:"sharedReadiness"`
}

// InspectWorktrees observes topology, not card state or readiness to enable sharing.
// It never creates a namespace, repairs worktrees, or changes reservations.
func InspectWorktrees(ctx context.Context, repo, board string) (WorktreeReport, error) {
	return inspectWorktrees(ctx, repo, board, nil)
}

func inspectWorktrees(ctx context.Context, repo, board string, afterRead func() error) (WorktreeReport, error) {
	if !utf8.ValidString(board) {
		return WorktreeReport{}, errors.New("board path must be UTF-8")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s, err := newScanner(repo, board)
	if err != nil {
		return WorktreeReport{}, err
	}
	if err := s.safe(ctx); err != nil {
		return WorktreeReport{}, err
	}
	boardInfo, err := s.inspectBoard()
	if err != nil {
		return WorktreeReport{}, err
	}
	common, commonInfo, err := s.commonDirectory(ctx)
	if err != nil {
		return WorktreeReport{}, err
	}
	before, err := s.run(ctx, "", "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return WorktreeReport{}, err
	}
	worktrees, err := parseWorktrees(before)
	if err != nil {
		return WorktreeReport{}, err
	}
	found := false
	for _, wt := range worktrees {
		if wt.Path == s.root && !wt.Bare && !wt.Prunable {
			found = true
		}
	}
	if !found {
		return WorktreeReport{}, errors.New("current repository absent from usable worktree inventory")
	}
	if afterRead != nil {
		if err := afterRead(); err != nil {
			return WorktreeReport{}, err
		}
	}
	if err := s.safe(ctx); err != nil {
		return WorktreeReport{}, err
	}
	afterBoard, err := s.inspectBoard()
	if err != nil || !os.SameFile(boardInfo, afterBoard) {
		return WorktreeReport{}, fmt.Errorf("board identity changed during inspection: %v", err)
	}
	afterCommon, afterCommonInfo, err := s.commonDirectory(ctx)
	if err != nil {
		return WorktreeReport{}, err
	}
	after, err := s.run(ctx, "", "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return WorktreeReport{}, err
	}
	if common != afterCommon || !os.SameFile(commonInfo, afterCommonInfo) || !bytes.Equal(before, after) {
		return WorktreeReport{}, errors.New("Git worktree topology changed during inspection; retry")
	}
	if err := ctx.Err(); err != nil {
		return WorktreeReport{}, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(s.board)))
	return WorktreeReport{SchemaVersion: 1, Repository: s.root, CommonDirectory: common,
		Board: s.board, NamespaceKey: key, Worktrees: worktrees, SharedReadiness: outputvocab.NotEvaluated}, nil
}

func (s scanner) commonDirectory(ctx context.Context) (string, fs.FileInfo, error) {
	raw, err := s.run(ctx, "", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", nil, err
	}
	dir := strings.TrimSuffix(string(raw), "\n")
	if !utf8.ValidString(dir) || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || strings.ContainsAny(dir, "\x00\r\n") {
		return "", nil, errors.New("invalid Git common directory path")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("Git common directory must be a real directory")
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", nil, err
	}
	return canonical, info, nil
}

// Exact directory names prevent case aliases from generating different keys on
// case-insensitive filesystems. A nested .git boundary belongs to another repo.
func (s scanner) inspectBoard() (fs.FileInfo, error) {
	r, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	parent := "."
	var info fs.FileInfo
	for _, component := range strings.Split(s.board, "/") {
		if strings.EqualFold(component, ".git") {
			return nil, errors.New("board cannot be inside Git metadata")
		}
		entries, err := fs.ReadDir(r.FS(), parent)
		if err != nil {
			return nil, err
		}
		found := false
		for _, entry := range entries {
			if entry.Name() == component {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("board component missing or case alias: %s", component)
		}
		parent = path.Join(parent, component)
		info, err = r.Lstat(parent)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("board path must contain only real directories")
		}
		if _, err := r.Lstat(path.Join(parent, ".git")); err == nil {
			return nil, errors.New("board crosses a nested Git repository boundary")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return info, nil
}
