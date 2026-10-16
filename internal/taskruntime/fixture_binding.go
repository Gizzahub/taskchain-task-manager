package taskruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrFixtureBinding = errors.New("synthetic execution identity, owner or generation mismatch")

// WithFixtureExecution is an inactive internal-library seam, not a run command.
// Both repository and common directory must be inside an explicitly selected
// private synthetic sandbox. The callback must only persist fixture declarations.
// Execution and receipt bytes are never rewritten and no provider is invoked.
func (s *Service) WithFixtureExecution(ctx context.Context, sandbox, repositoryID, common string, captured Execution, persist func() error) error {
	r, err := OpenFixtureSandbox(sandbox)
	if err != nil {
		return err
	}
	defer r.Close()
	inside := func(p string) bool {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return false
		}
		rel, e := filepath.Rel(sandbox, p)
		if e != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		path := ""
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			path = filepath.Join(path, part)
			i, err := r.Lstat(path)
			if err != nil || !fixtureSafeDirectory(i) {
				return false
			}
		}
		return true
	}
	if !inside(s.root) || !inside(common) || !inside(captured.Worktree) || persist == nil {
		return ErrFixtureBinding
	}
	store, err := s.store(ctx)
	if err != nil || store.dir != newStateStore(common).dir {
		return ErrFixtureBinding
	}
	identity, err := os.Lstat(common)
	if err != nil || !identity.IsDir() || identity.Mode()&os.ModeSymlink != 0 {
		return ErrFixtureBinding
	}
	store, checkStatePath, err := store.pinFixture(r, sandbox)
	if err != nil {
		return err
	}
	defer store.fixtureRoot.Close()
	unlock, err := store.lock()
	if err != nil {
		return ErrFixtureBinding
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg, err := s.config()
	if err != nil || cfg.RepositoryID != repositoryID {
		return ErrFixtureBinding
	}
	currentStore, err := s.store(ctx)
	currentIdentity, statErr := os.Lstat(common)
	if err != nil || currentStore.dir != store.dir || statErr != nil || !os.SameFile(identity, currentIdentity) {
		return ErrFixtureBinding
	}
	owner, _, err := s.resolveOwner(ctx)
	if err != nil || owner != captured.Owner {
		return ErrFixtureBinding
	}
	data, err := store.fsReadFile(store.executionsPath())
	if err != nil {
		return ErrFixtureBinding
	}
	// Validate through the existing reader, then retain literal legacy purpose
	// from those same bytes for the new snapshot's stricter equality contract.
	var validated []Execution
	if json.Unmarshal(data, &validated) != nil {
		return ErrFixtureBinding
	}
	type literalExecution Execution
	var executions []literalExecution
	if json.Unmarshal(data, &executions) != nil {
		return ErrFixtureBinding
	}
	// Preserve the literal tuple, including an omitted legacy purpose. Current
	// runtime receipt helpers intentionally normalize purpose for other commands.
	found := false
	count := 0
	for _, e := range executions {
		if e.Task == captured.Task {
			count++
		}
		if e.Task == captured.Task && e.Type == captured.Type && e.Purpose == captured.Purpose && e.Source == captured.Source && e.Owner == captured.Owner && e.Branch == captured.Branch && e.Worktree == captured.Worktree && e.StartedAt.Equal(captured.StartedAt) {
			found = true
		}
	}
	if !found || count != 1 {
		return ErrFixtureBinding
	}
	latest, err := store.latestReceipts()
	if err != nil {
		return ErrFixtureBinding
	}
	if receipt, ok := latest[captured.Task]; ok && !receipt.CreatedAt.Before(captured.StartedAt) && (receipt.Status == StatusDone || receipt.Status == StatusAborted) {
		return ErrFixtureBinding
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkStatePath(); err != nil {
		return err
	}
	return persist()
}
