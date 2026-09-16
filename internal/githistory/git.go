package githistory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const maxOutput = 64 << 20

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("Git output limit exceeded")
	}
	return b.Buffer.Write(p)
}

type scanner struct{ root, board string }

func (s scanner) run(ctx context.Context, input string, args ...string) ([]byte, error) {
	base := []string{"--no-pager", "--no-replace-objects", "--no-lazy-fetch", "--literal-pathspecs", "-c", "log.showSignature=false", "-c", "core.fsmonitor=false"}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = s.root
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, diag := cappedBuffer{limit: maxOutput}, cappedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = &out, &diag
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(diag.String()))
	}
	if diag.Len() != 0 {
		return nil, fmt.Errorf("git %s produced diagnostics: %s", args[0], strings.TrimSpace(diag.String()))
	}
	return out.Bytes(), nil
}

func newScanner(repo, board string) (scanner, error) {
	if repo == "" || board == "" || board == "." || board == ".." || path.IsAbs(board) || path.Clean(board) != board || strings.HasPrefix(board, "../") || strings.ContainsAny(board, "\\\x00\r\n") {
		return scanner{}, errors.New("require repository root and clean repository-relative board path")
	}
	root, err := filepath.Abs(repo)
	if err != nil {
		return scanner{}, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return scanner{}, errors.New("repository root must be a real directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return scanner{}, err
	}
	return scanner{root: root, board: board}, nil
}

func (s scanner) safe(ctx context.Context) error {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_TRACE") && os.Getenv(key) != "" {
			return fmt.Errorf("cannot scan with %s override", key)
		}
	}
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_GRAFT_FILE", "GIT_REPLACE_REF_BASE", "GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL", "GIT_EXEC_PATH", "GIT_SHALLOW_FILE", "GIT_NAMESPACE", "GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS"} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("cannot scan with %s override", key)
		}
	}
	root, err := s.run(ctx, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if filepath.Clean(strings.TrimSpace(string(root))) != s.root {
		return errors.New("repo must name the Git worktree root")
	}
	shallow, err := s.run(ctx, "", "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(shallow)) != "false" {
		return errors.New("cannot scan shallow repository")
	}
	replaced, err := s.run(ctx, "", "for-each-ref", "--format=%(refname)", "refs/replace")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(replaced)) != 0 {
		return errors.New("cannot scan while replace refs exist")
	}
	grafts, err := s.run(ctx, "", "rev-parse", "--path-format=absolute", "--git-path", "info/grafts")
	if err != nil {
		return err
	}
	if info, err := os.Lstat(strings.TrimSpace(string(grafts))); err == nil {
		if !info.Mode().IsRegular() || info.Size() != 0 {
			return errors.New("cannot scan while grafts exist")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	partial, err := s.run(ctx, "", "config", "--get-regexp", `^(extensions\.[pP]artial[cC]lone|remote\..*\.[pP]romisor)$`)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return err
		}
	} else if len(bytes.TrimSpace(partial)) != 0 {
		return errors.New("cannot scan partial/promisor repository")
	}
	return nil
}
