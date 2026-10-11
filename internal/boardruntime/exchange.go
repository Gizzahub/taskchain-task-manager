package boardruntime

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const receiptName = ".ce-maintenance-receipt.json"

type exchangeRequest struct {
	Root         string
	Plan         MaintenancePlan
	Files        map[string][]byte
	Receipt      []byte
	Precondition func() error
}

type exchangeResult struct {
	Committed bool
	Stage     string
}

// exchangeBoard copies the board to a sibling stage, writes the receipt inside
// that stage, and swaps the two directories. There is no two-rename fallback.
// After a successful swap, a later error is ErrCommitted.
func exchangeBoard(req exchangeRequest) (exchangeResult, error) {
	if req.Precondition == nil {
		return exchangeResult{}, fmt.Errorf("explicit caller precondition is required")
	}
	if len(req.Receipt) == 0 {
		return exchangeResult{}, fmt.Errorf("receipt is required")
	}
	board := filepath.Join(req.Root, filepath.FromSlash(req.Plan.Board))
	info, err := os.Lstat(board)
	if err != nil {
		return exchangeResult{}, fmt.Errorf("board: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return exchangeResult{}, fmt.Errorf("board must be a real directory")
	}
	if _, statErr := os.Lstat(filepath.Join(board, receiptName)); statErr == nil {
		return exchangeResult{}, fmt.Errorf("board already contains maintenance receipt")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return exchangeResult{}, statErr
	}
	parent := filepath.Dir(board)
	if err := probeExchange(parent); err != nil {
		return exchangeResult{}, err
	}
	stage, err := os.MkdirTemp(parent, ".ce-board-stage-")
	if err != nil {
		return exchangeResult{}, fmt.Errorf("create board stage: %w", err)
	}
	result := exchangeResult{Stage: stage}
	if err := copyBoard(board, stage); err != nil {
		return result, fmt.Errorf("stage board retained after failure: %w", err)
	}
	for _, file := range req.Plan.Before {
		if file.Kind != "card" {
			continue
		}
		rel := strings.TrimPrefix(file.Path, req.Plan.Board+"/")
		if err := os.Remove(filepath.Join(stage, filepath.FromSlash(rel))); err != nil {
			return result, fmt.Errorf("stage board retained after failure: remove staged card %s: %w", file.Path, err)
		}
	}
	for path, data := range req.Files {
		rel := strings.TrimPrefix(path, req.Plan.Board+"/")
		target := filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return result, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return result, fmt.Errorf("stage board retained after failure: write staged card %s: %w", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, receiptName), req.Receipt, 0o600); err != nil {
		return result, fmt.Errorf("stage board retained after failure: write staged receipt: %w", err)
	}
	if err := req.Precondition(); err != nil {
		return result, fmt.Errorf("caller precondition: %w", err)
	}
	parentFD, err := os.Open(parent)
	if err != nil {
		return result, err
	}
	defer func() { _ = parentFD.Close() }()
	if err := exchangeDirectories(int(parentFD.Fd()), filepath.Base(board), filepath.Base(stage)); err != nil {
		return result, fmt.Errorf("exchange board: %w", err)
	}
	result.Committed = true
	if err := parentFD.Sync(); err != nil {
		return result, fmt.Errorf("%w: sync exchanged board parent: %v", ErrCommitted, err)
	}
	if TestingAfterExchange != nil {
		if err := TestingAfterExchange(); err != nil {
			return result, fmt.Errorf("%w: %v", ErrCommitted, err)
		}
	}
	return result, nil
}

func copyBoard(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("board contains symlink: %s", rel)
		}
		if info.IsDir() {
			if rel == "." {
				return nil
			}
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("board contains non-regular file: %s", rel)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
