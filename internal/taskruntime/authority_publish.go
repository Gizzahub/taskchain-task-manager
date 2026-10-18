package taskruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errAuthorityPublicationConflict = errors.New("authority publication conflict")
var errAuthorityPublicationRefused = errors.New("authority publication refused")

type authorityPublicationInput struct {
	repository        canonicalRepository
	expectedRaw       []byte
	expectedAuthority canonicalLifecycleAuthority // zero means protected legacy-absent
	targetRaw         []byte
	targetGeneration  string
}

// No production constructor exists. Only a future separately accepted owner
// controller may mint this process-local scope. Tests construct a synthetic
// controller in a new temporary repository; JSON, actors and ordinary mutators
// cannot construct this unexported capability through any public interface.
type ownerTransitionScope struct {
	controller       *authorityTransitionController
	confirmationOnly bool
}

type authorityTransitionController struct {
	repository        canonicalRepository
	expectedRaw       []byte
	expectedAuthority canonicalLifecycleAuthority
	target            []byte
	artifactIdentity  string
	approved          bool
	frozen            bool
	drained           bool
	// External callers, board pending/protection and legacy cleanup belong to
	// their owners. Missing knowledge must refuse, never imply quiescence.
	quiescence func(context.Context) (authorityQuiescence, error)
}

type authorityPublicationResult struct {
	status      string
	observation canonicalAuthorityObservation // observation only, never a mutation lease
}

func (scope *ownerTransitionScope) validate(ctx context.Context, s *Service, in authorityPublicationInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scope == nil || scope.controller == nil {
		return errAuthorityPublicationRefused
	}
	c := scope.controller
	if !c.approved || c.artifactIdentity == "" || !bytes.Equal(c.expectedRaw, in.expectedRaw) || c.expectedAuthority != in.expectedAuthority || !bytes.Equal(c.target, in.targetRaw) ||
		!sameRepositoryIdentity(c.repository.primary, in.repository.primary) ||
		!sameRepositoryIdentity(c.repository.caller, in.repository.caller) ||
		!bytes.Equal(c.repository.inventory, in.repository.inventory) {
		return errAuthorityPublicationRefused
	}
	if err := in.repository.revalidate(ctx, s); err != nil {
		return fmt.Errorf("%w: %v", errAuthorityPublicationRefused, err)
	}
	return nil
}

// publishCanonicalAuthority is inactive: no Service verb, CLI or installer
// calls it. It owns one existing mutation lock; it never invokes a provider,
// writes a receipt, rolls back a rename, or releases controller freeze ownership.
func (s *Service) publishCanonicalAuthority(ctx context.Context, in authorityPublicationInput, scope *ownerTransitionScope) (authorityPublicationResult, error) {
	return s.publishCanonicalAuthorityHook(ctx, in, scope, nil)
}

func (s *Service) publishCanonicalAuthorityHook(ctx context.Context, in authorityPublicationInput, scope *ownerTransitionScope, hook canonicalReadHook) (result authorityPublicationResult, err error) {
	result.status = "refused"
	if err = scope.validate(ctx, s, in); err != nil {
		return result, err
	}
	target, err := decodeCanonicalAuthority(in.targetRaw)
	if err != nil {
		return result, err
	}
	if target.Authority.Generation != in.targetGeneration {
		return result, errAuthorityPublicationRefused
	}
	expected, legacy, err := decodePublicationCurrent(in.expectedRaw)
	if err != nil {
		return result, err
	}
	if expected.Authority != in.expectedAuthority || (legacy && in.expectedAuthority != (canonicalLifecycleAuthority{})) ||
		expected.Config.RepositoryID != target.Config.RepositoryID {
		return result, errAuthorityPublicationRefused
	}
	// The protected precheck must succeed before creating runtime state/lock.
	if err = s.withPublicationConfig(ctx, in.repository, hook, func(canonicalConfigHandle) error { return nil }); err != nil {
		return result, err
	}
	store := newStateStore(in.repository.primary.commonDir)
	lock, err := store.lockOwnedHook(hook)
	if err != nil {
		return result, err
	}
	defer func() {
		if e := lock.release(); e != nil {
			err = errors.Join(err, fmt.Errorf("release publication lock: %w", e))
		}
		if err != nil && (result.status == "published" || result.status == "already-published") {
			result.status = "publication-unverified"
			result.observation = canonicalAuthorityObservation{}
		}
	}()
	err = s.withPublicationConfig(ctx, in.repository, hook, func(h canonicalConfigHandle) error {
		if e := lock.validate(); e != nil {
			return e
		}
		if e := scope.validate(ctx, s, in); e != nil {
			return e
		}
		// Exact-target confirmation precedes old CAS and NEW transition checks.
		if bytes.Equal(h.raw, in.targetRaw) && h.declaration.Authority == target.Authority {
			if e := syncPublicationCurrent(h, hook); e != nil {
				result.status = "durability-unknown"
				return e
			}
			if e := lock.validate(); e != nil {
				return e
			}
			if e := scope.validate(ctx, s, in); e != nil {
				return e
			}
			result = authorityPublicationResult{"already-published", canonicalAuthorityObservation{in.repository, append([]byte(nil), h.raw...), target}}
			return nil
		}
		result.status = "conflict"
		if scope.confirmationOnly || !bytes.Equal(h.raw, in.expectedRaw) || h.declaration.Authority != in.expectedAuthority {
			return errAuthorityPublicationConflict
		}
		if !legacy && target.Authority.Generation == expected.Authority.Generation {
			return fmt.Errorf("%w: new revision requires new generation", errAuthorityPublicationConflict)
		}
		result.status = "refused"
		if e := checkAuthorityQuiescence(ctx, lock, scope.controller); e != nil {
			return e
		}
		if e := scope.validate(ctx, s, in); e != nil {
			return e
		}
		result.status = "failed-before-publish"
		return s.publishPinnedAuthority(ctx, h, in, scope, lock, target, hook, &result)
	})
	if err != nil && (result.status == "published" || result.status == "already-published") {
		result.status = "publication-unverified"
		result.observation = canonicalAuthorityObservation{}
	}
	return result, err
}

// The same protected FD reader is used for legacy bootstrap and schema6.
// Only explicit schema2/3 with no-fetch is legacy-absent; schema4/5 and broken
// schema6 can never fall through into bootstrap.
func decodePublicationCurrent(raw []byte) (canonicalAuthorityDeclaration, bool, error) {
	if d, err := decodeCanonicalAuthority(raw); err == nil {
		return d, false, nil
	}
	cfg, err := decodeRuntimeConfig(raw)
	if err != nil || (cfg.SchemaVersion != 2 && cfg.SchemaVersion != 3) || cfg.IntegrationNetworkPolicy != NetworkNoFetch {
		return canonicalAuthorityDeclaration{}, false, errCanonicalInvalid
	}
	return canonicalAuthorityDeclaration{Config: cfg}, true, nil
}

func (s *Service) withPublicationConfig(ctx context.Context, expected canonicalRepository, hook canonicalReadHook, cb canonicalConfigCallback) error {
	// Preserve the canonical caller-path check, including symlinks.
	abs, err := filepath.Abs(s.root)
	if err != nil {
		return errCanonicalUnavailable
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil || physical != abs {
		return errCanonicalUnavailable
	}
	if err := expected.revalidate(ctx, s); err != nil {
		return err
	}
	return withPublicationConfigPath(expected, hook, func(h canonicalConfigHandle) error {
		if err := expected.revalidate(ctx, s); err != nil {
			return err
		}
		return cb(h)
	})
}

func syncPublicationCurrent(h canonicalConfigHandle, hook canonicalReadHook) error {
	if err := h.config.Sync(); err != nil {
		return err
	}
	if err := canonicalReadPoint(hook, "publication-config-sync"); err != nil {
		return err
	}
	if err := h.parent.Sync(); err != nil {
		return err
	}
	if err := canonicalReadPoint(hook, "publication-directory-sync"); err != nil {
		return err
	}
	return recheckCanonicalConfigPath(h.repository.primary.root, h.config, h.raw)
}

func withPublicationConfigPath(repo canonicalRepository, hook canonicalReadHook, cb canonicalConfigCallback) error {
	return withCanonicalConfigPath(repo.primary.root, hook, func(file, parent *os.File, raw []byte) error {
		decl, _, err := decodePublicationCurrent(raw)
		if err != nil {
			return err
		}
		return cb(canonicalConfigHandle{repository: repo, raw: append([]byte(nil), raw...), declaration: decl, config: file, parent: parent})
	})
}
