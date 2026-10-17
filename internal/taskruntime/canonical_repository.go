package taskruntime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

// canonicalRepository is an inactive topology observation, never a grant or a
// protected config read. No run command consumes it yet. A future locked reader
// must revalidate it AND separately prove config permissions, ACLs and identity.
type canonicalRepository struct {
	caller, primary repositoryIdentity
	inventory       []byte
}

type repositoryIdentity struct {
	root, gitDir, commonDir       string
	rootInfo, gitInfo, commonInfo os.FileInfo
}

// canonicalRepository resolves primary by Git identity rather than directory
// naming or inventory order. It never falls back to a linked config, writes a
// lock, reads YAML, invokes a provider or changes the inventory.
func (s *Service) canonicalRepository(ctx context.Context) (canonicalRepository, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	before, err := s.captureCanonicalRepository(ctx)
	if err != nil {
		return canonicalRepository{}, err
	}
	if err := before.revalidate(ctx, s); err != nil {
		return canonicalRepository{}, err
	}
	return before, nil
}

func (c canonicalRepository) configPath() string {
	return filepath.Join(c.primary.root, ConfigFile)
}

func (c canonicalRepository) revalidate(ctx context.Context, s *Service) error {
	after, err := s.captureCanonicalRepository(ctx)
	if err != nil {
		return err
	}
	if !sameRepositoryIdentity(c.caller, after.caller) || !sameRepositoryIdentity(c.primary, after.primary) || !bytes.Equal(c.inventory, after.inventory) {
		return fmt.Errorf("canonical repository identity or worktree inventory changed")
	}
	return nil
}

func sameRepositoryIdentity(a, b repositoryIdentity) bool {
	return a.root == b.root && a.gitDir == b.gitDir && a.commonDir == b.commonDir &&
		a.rootInfo != nil && a.gitInfo != nil && a.commonInfo != nil &&
		os.SameFile(a.rootInfo, b.rootInfo) && os.SameFile(a.gitInfo, b.gitInfo) && os.SameFile(a.commonInfo, b.commonInfo)
}

func (s *Service) captureCanonicalRepository(ctx context.Context) (canonicalRepository, error) {
	var zero canonicalRepository
	// These variables can redirect Git away from the selected checkout. Reject
	// rather than silently resolve another repository or alter the caller's env.
	for _, key := range []string{"GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		if os.Getenv(key) != "" {
			return zero, fmt.Errorf("canonical repository unavailable: %s redirects Git", key)
		}
	}
	caller, err := s.repositoryIdentity(ctx, s.root)
	if err != nil {
		return zero, err
	}
	raw, err := s.canonicalGit(ctx, caller.root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return zero, err
	}
	worktrees, err := githistory.ParseWorktreeInventory(raw)
	if err != nil {
		return zero, fmt.Errorf("canonical worktree inventory: %w", err)
	}
	var primary repositoryIdentity
	primaryCount, callerCount := 0, 0
	for _, wt := range worktrees {
		if wt.Bare || wt.Prunable {
			return zero, fmt.Errorf("canonical repository requires an available non-bare inventory")
		}
		identity, err := s.repositoryIdentity(ctx, wt.Path)
		if err != nil {
			return zero, err
		}
		listed, err := filepath.EvalSymlinks(wt.Path)
		if err != nil || listed != identity.root || !os.SameFile(caller.commonInfo, identity.commonInfo) {
			return zero, fmt.Errorf("canonical inventory entry does not belong to the selected common directory")
		}
		if identity.root == caller.root {
			callerCount++
		}
		if identity.gitDir == identity.commonDir && os.SameFile(identity.gitInfo, identity.commonInfo) {
			primary, primaryCount = identity, primaryCount+1
		} else {
			// Lstat includes dangling symlinks: even identical or unreadable local
			// copies cannot shadow the primary declaration. Never remove them.
			if _, err := os.Lstat(filepath.Join(identity.root, ConfigFile)); !os.IsNotExist(err) {
				return zero, fmt.Errorf("non-primary runtime config shadow is present or unavailable")
			}
		}
	}
	if primaryCount != 1 || callerCount != 1 {
		return zero, fmt.Errorf("canonical repository requires exactly one primary and one caller inventory entry")
	}
	return canonicalRepository{caller: caller, primary: primary, inventory: append([]byte(nil), raw...)}, nil
}

func (s *Service) repositoryIdentity(ctx context.Context, root string) (repositoryIdentity, error) {
	var zero repositoryIdentity
	bare, err := s.canonicalGit(ctx, root, "rev-parse", "--is-bare-repository")
	if err != nil {
		return zero, err
	}
	if string(bare) != "false\n" {
		return zero, fmt.Errorf("canonical repository is unavailable or bare")
	}
	var paths [3]string
	for i, flag := range []string{"--show-toplevel", "--absolute-git-dir", "--git-common-dir"} {
		out, err := s.canonicalGit(ctx, root, "rev-parse", "--path-format=absolute", flag)
		if err != nil {
			return zero, err
		}
		path := strings.TrimSuffix(string(out), "\n")
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00") {
			return zero, fmt.Errorf("canonical Git path is not an unambiguous absolute path")
		}
		paths[i], err = filepath.EvalSymlinks(path)
		if err != nil {
			return zero, fmt.Errorf("canonical Git path unavailable: %w", err)
		}
	}
	var infos [3]os.FileInfo
	for i, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return zero, fmt.Errorf("canonical Git directory unavailable: %s", path)
		}
		infos[i] = info
	}
	return repositoryIdentity{paths[0], paths[1], paths[2], infos[0], infos[1], infos[2]}, nil
}

func (s *Service) canonicalGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	result, err := s.runner.Run(ctx, CommandRequest{Command: "git", Args: args, WorkDir: root, Env: map[string]string{"GIT_OPTIONAL_LOCKS": "0"}})
	if err != nil {
		return nil, fmt.Errorf("canonical Git query: %w", err)
	}
	if result.ExitCode != 0 || result.NotStarted {
		return nil, fmt.Errorf("canonical Git query failed: %s", strings.TrimSpace(result.Stderr))
	}
	return []byte(result.Stdout), nil
}
