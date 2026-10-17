package taskruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const canonicalConfigMaxBytes = 64 << 10

var (
	errCanonicalUnavailable = errors.New("canonical config unavailable")
	errCanonicalInvalid     = errors.New("invalid canonical authority profile")
)

// This observation is not a grant, durable token or serialization format. No
// runtime command consumes it. Future grant users must revalidate under the
// existing mutation lock and complete the separate sync/authority protocol.
type canonicalAuthorityObservation struct {
	repository  canonicalRepository
	raw         []byte
	declaration canonicalAuthorityDeclaration
}

type canonicalReadHook func(string) error

// canonicalConfigHandle owns the protected descriptor for the duration of a
// synchronous callback. It is deliberately process-local and cannot be
// reconstructed from the observation fields.
type canonicalConfigHandle struct {
	repository  canonicalRepository
	raw         []byte
	declaration canonicalAuthorityDeclaration
	config      *os.File
	parent      *os.File
}

type canonicalConfigCallback func(canonicalConfigHandle) error

func canonicalReadPoint(hook canonicalReadHook, point string) error {
	if hook != nil {
		return hook(point)
	}
	return nil
}

func (s *Service) readCanonicalAuthority(ctx context.Context) (canonicalAuthorityObservation, error) {
	return s.readCanonicalAuthorityWith(ctx, nil)
}

func (s *Service) readCanonicalAuthorityWith(ctx context.Context, hook canonicalReadHook) (canonicalAuthorityObservation, error) {
	var zero canonicalAuthorityObservation
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	point := func(name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return canonicalReadPoint(hook, name)
	}
	// Git's resolver canonicalizes paths; do not erase a symlink supplied at
	// this stronger filesystem boundary before the no-follow walk.
	abs, err := filepath.Abs(s.root)
	if err != nil {
		return zero, fmt.Errorf("%w: caller path", errCanonicalUnavailable)
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil || physical != abs {
		return zero, fmt.Errorf("%w: noncanonical caller path", errCanonicalUnavailable)
	}
	repo, err := s.canonicalRepository(ctx)
	if err != nil {
		return zero, fmt.Errorf("%w: %w", errCanonicalUnavailable, err)
	}
	data, err := readCanonicalConfig(repo.primary.root, point)
	if err != nil {
		return zero, err
	}
	decl, err := decodeCanonicalAuthority(data)
	if err != nil {
		return zero, err
	}
	if err := point("before-git-revalidation"); err != nil {
		return zero, fmt.Errorf("%w: %w", errCanonicalUnavailable, err)
	}
	if err := repo.revalidate(ctx, s); err != nil {
		return zero, fmt.Errorf("%w: %w", errCanonicalUnavailable, err)
	}
	return canonicalAuthorityObservation{repo, data, decl}, nil
}

// withCanonicalConfig keeps the validated config and its owner-private parent
// descriptor open while callback runs. The callback is synchronous by
// contract; no durable grant or legacy transition is produced here.
func (s *Service) withCanonicalConfig(ctx context.Context, hook canonicalReadHook, callback canonicalConfigCallback) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	abs, err := filepath.Abs(s.root)
	if err != nil {
		return fmt.Errorf("%w: caller path", errCanonicalUnavailable)
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil || physical != abs {
		return fmt.Errorf("%w: noncanonical caller path", errCanonicalUnavailable)
	}
	repo, err := s.canonicalRepository(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errCanonicalUnavailable, err)
	}
	return withCanonicalConfigPath(repo.primary.root, hook, func(config *os.File, parent *os.File, raw []byte) error {
		decl, err := decodeCanonicalAuthority(raw)
		if err != nil {
			if legacy, legacyErr := decodeRuntimeConfig(raw); legacyErr == nil && legacy.SchemaVersion <= 3 {
				return errAuthorityLegacy
			}
			return err
		}
		if err := repo.revalidate(ctx, s); err != nil {
			return fmt.Errorf("%w: %w", errCanonicalUnavailable, err)
		}
		return callback(canonicalConfigHandle{repository: repo, raw: append([]byte(nil), raw...), declaration: decl, config: config, parent: parent})
	})
}
