package taskruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// authorityLease is an in-process, callback-scoped capability. It contains
// no serializable token and is valid only while its owned mutation lock and
// pinned canonical descriptor remain unchanged.
type authorityLease struct {
	lock    *ownedMutationLock
	config  canonicalConfigHandle
	engine  string
	service *Service
	ctx     context.Context
	valid   bool
}

func (l *authorityLease) validate() error {
	if l == nil || !l.valid || l.lock == nil || l.lock.released || l.config.config == nil {
		return errAuthorityLeaseExpired
	}
	current, err := l.lock.store.fsReadFile(l.lock.path)
	if err != nil || !bytes.Equal(current, l.lock.data) {
		return fmt.Errorf("%w: mutation lock changed", errAuthorityLeaseExpired)
	}
	st, err := os.Lstat(l.lock.path)
	if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() || !os.SameFile(l.lock.info, st) {
		return fmt.Errorf("%w: mutation lock identity changed", errAuthorityLeaseExpired)
	}
	var currentStat os.FileInfo
	if currentStat, err = l.config.config.Stat(); err != nil || currentStat.Size() != int64(len(l.config.raw)) {
		return fmt.Errorf("%w: canonical descriptor changed", errAuthorityLeaseExpired)
	}
	pathStat, err := os.Stat(l.config.repository.configPath())
	if err != nil || !os.SameFile(currentStat, pathStat) {
		return fmt.Errorf("%w: canonical path identity changed", errAuthorityLeaseExpired)
	}
	if l.service != nil {
		if err := l.config.repository.revalidate(l.ctx, l.service); err != nil {
			return fmt.Errorf("%w: repository identity changed: %v", errAuthorityLeaseExpired, err)
		}
	}
	if err := recheckCanonicalConfigPath(l.config.repository.primary.root, l.config.config, l.config.raw); err != nil {
		return fmt.Errorf("%w: %v", errAuthorityLeaseExpired, err)
	}
	return nil
}

// withAuthorityMutation owns precheck, one existing mutation lock, the
// already-locked validator and release. It remains inactive because no caller
// in the live service invokes it yet.
func (s *Service) withAuthorityMutation(ctx context.Context, expectedEngine string, callback func(*authorityLease) error) error {
	return s.withAuthorityMutationHook(ctx, expectedEngine, nil, callback)
}

func (s *Service) withAuthorityMutationHook(ctx context.Context, expectedEngine string, hook canonicalReadHook, callback func(*authorityLease) error) (err error) {
	var writer string
	if err := s.withCanonicalConfig(ctx, hook, func(h canonicalConfigHandle) error {
		writer = h.declaration.Authority.Writer
		return nil
	}); err != nil {
		return err
	}
	if writer != expectedEngine || (writer != "ce" && writer != "task-manager") {
		return fmt.Errorf("%w: got %q, want %q", errAuthorityWriter, writer, expectedEngine)
	}
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	lock, err := store.lockOwnedHook(hook)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release authority mutation: %w", releaseErr))
		}
	}()
	return s.withLockedCanonicalAuthorityHook(ctx, lock, expectedEngine, hook, callback)
}

// withLockedCanonicalAuthority assumes the caller owns the existing lock and
// never acquires or releases it. Config and parent directory sync complete
// before the callback receives its process-local lease.
func (s *Service) withLockedCanonicalAuthority(ctx context.Context, lock *ownedMutationLock, expectedEngine string, callback func(*authorityLease) error) error {
	return s.withLockedCanonicalAuthorityHook(ctx, lock, expectedEngine, nil, callback)
}

func (s *Service) withLockedCanonicalAuthorityHook(ctx context.Context, lock *ownedMutationLock, expectedEngine string, hook canonicalReadHook, callback func(*authorityLease) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if lock == nil || lock.released {
		return errAuthorityLeaseExpired
	}
	if err := lock.validate(); err != nil {
		return err
	}
	return s.withCanonicalConfig(ctx, hook, func(h canonicalConfigHandle) error {
		if h.declaration.Authority.Writer != expectedEngine || (expectedEngine != "ce" && expectedEngine != "task-manager") {
			return fmt.Errorf("%w: got %q, want %q", errAuthorityWriter, h.declaration.Authority.Writer, expectedEngine)
		}
		if err := lock.validate(); err != nil {
			return err
		}
		if lock.store.dir != newStateStore(h.repository.primary.commonDir).dir {
			return fmt.Errorf("%w: mutation lock is bound to another repository", errAuthorityLeaseExpired)
		}
		if err := h.config.Sync(); err != nil {
			return fmt.Errorf("sync canonical config: %w", err)
		}
		if err := canonicalReadPoint(hook, "config-sync"); err != nil {
			return fmt.Errorf("sync canonical config: %w", err)
		}
		if err := h.parent.Sync(); err != nil {
			return fmt.Errorf("sync canonical config directory: %w", err)
		}
		if err := canonicalReadPoint(hook, "parent-sync"); err != nil {
			return fmt.Errorf("sync canonical config directory: %w", err)
		}
		lease := &authorityLease{lock: lock, config: h, engine: expectedEngine, service: s, ctx: ctx, valid: true}
		defer func() { lease.valid = false }()
		if err := lease.validate(); err != nil {
			return err
		}
		return callback(lease)
	})
}

func (l *ownedMutationLock) validate() error {
	if l == nil || l.released {
		return errAuthorityLeaseExpired
	}
	current, err := l.store.fsReadFile(l.path)
	if err != nil || !bytes.Equal(current, l.data) {
		return fmt.Errorf("%w: mutation lock changed", errAuthorityLeaseExpired)
	}
	st, err := os.Lstat(l.path)
	if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() || !os.SameFile(l.info, st) {
		return fmt.Errorf("%w: mutation lock identity changed", errAuthorityLeaseExpired)
	}
	return nil
}
