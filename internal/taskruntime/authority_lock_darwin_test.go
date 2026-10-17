//go:build darwin && cgo

package taskruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthorityLockedBoundary(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	s := NewService(primary, NewExecutor())
	repo, err := s.canonicalRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := newStateStore(repo.primary.commonDir)
	if _, err := os.Stat(store.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("lock exists before precheck: %v", err)
	}
	if err := s.withAuthorityMutation(context.Background(), "task-manager", func(*authorityLease) error { return nil }); !errors.Is(err, errAuthorityWriter) {
		t.Fatalf("none writer: %v", err)
	}
	if _, err := os.Stat(store.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("none precheck created lock: %v", err)
	}

	data := strings.Replace(canonicalTestProfile, "writer: none", "writer: task-manager", 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	var lease *authorityLease
	if err := s.withAuthorityMutation(context.Background(), "task-manager", func(got *authorityLease) error {
		lease = got
		return got.validate()
	}); err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.validate() == nil {
		t.Fatal("lease survived callback")
	}
	if _, err := os.Stat(store.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("owned lock remained: %v", err)
	}
}

func TestAuthorityLockedBoundaryForeignWriterHasNoMutation(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	data := strings.Replace(canonicalTestProfile, "writer: none", "writer: ce", 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewService(primary, NewExecutor())
	repo, err := s.canonicalRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := newStateStore(repo.primary.commonDir)
	if err := s.withAuthorityMutation(context.Background(), "task-manager", func(*authorityLease) error { return nil }); !errors.Is(err, errAuthorityWriter) {
		t.Fatalf("foreign writer: %v", err)
	}
	if _, err := os.Stat(store.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("foreign precheck created lock: %v", err)
	}
}

func TestAuthorityLockedBoundarySameBytesLockReplacementIsPreserved(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	data := strings.Replace(canonicalTestProfile, "writer: none", "writer: task-manager", 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewService(primary, NewExecutor())
	var lease *authorityLease
	err := s.withAuthorityMutation(context.Background(), "task-manager", func(got *authorityLease) error {
		lease = got
		if err := os.Rename(got.lock.path, got.lock.path+".replaced"); err != nil {
			return err
		}
		if err := os.WriteFile(got.lock.path, got.lock.data, 0o644); err != nil {
			return err
		}
		return got.validate()
	})
	if err == nil || lease == nil {
		t.Fatalf("replacement accepted: %v", err)
	}
	if _, statErr := os.Stat(lease.lock.path + ".replaced"); statErr != nil {
		t.Fatal(statErr)
	}
}

func TestAuthorityLockedBoundaryLegacyIsExplicitlyRejected(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	legacy := `schema-version: 3
repository-id: synthetic
worktree-roots:
  synthetic-host: /synthetic/worktrees/synthetic
integration-provider: gz-git
integration-network-policy: no-fetch
execution-administrators: []
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewService(primary, NewExecutor())
	if err := s.withAuthorityMutation(context.Background(), "task-manager", func(*authorityLease) error { return nil }); !errors.Is(err, errAuthorityLegacy) {
		t.Fatalf("legacy classification: %v", err)
	}
}

func TestAuthorityLockedBoundaryConfigReplacementIsNotGranted(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	data := strings.Replace(canonicalTestProfile, "writer: none", "writer: task-manager", 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewService(primary, NewExecutor())
	err := s.withAuthorityMutation(context.Background(), "task-manager", func(lease *authorityLease) error {
		if err := os.Rename(path, path+".old"); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			return err
		}
		return lease.validate()
	})
	if err == nil {
		t.Fatal("replacement accepted")
	}
	if _, statErr := os.Stat(filepath.Join(primary, ConfigFile+".old")); statErr != nil {
		t.Fatal(statErr)
	}
}
