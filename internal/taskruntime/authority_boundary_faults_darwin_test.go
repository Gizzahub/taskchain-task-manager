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

func authorityBoundaryFixture(t *testing.T) (*Service, stateStore, string) {
	t.Helper()
	primary, _, path := canonicalConfigFixture(t)
	data := strings.Replace(canonicalTestProfile, "writer: none", "writer: task-manager", 1)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewService(primary, NewExecutor())
	repo, err := s.canonicalRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, newStateStore(repo.primary.commonDir), path
}

func TestAuthorityLockedBoundaryPrecheckCreatesNoStore(t *testing.T) {
	for _, writer := range []string{"none", "ce"} {
		t.Run(writer, func(t *testing.T) {
			s, store, path := authorityBoundaryFixture(t)
			data := strings.Replace(canonicalTestProfile, "writer: none", "writer: "+writer, 1)
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			called := false
			err := s.withAuthorityMutation(context.Background(), "task-manager", func(*authorityLease) error { called = true; return nil })
			if !errors.Is(err, errAuthorityWriter) || called {
				t.Fatalf("ineligible writer called mutation: called=%v err=%v", called, err)
			}
			if _, err := os.Stat(store.dir); !os.IsNotExist(err) {
				t.Fatalf("precheck created store: %v", err)
			}
		})
	}
}

func TestAuthorityLockedBoundarySyncFailures(t *testing.T) {
	for _, point := range []string{"config-sync", "parent-sync"} {
		t.Run(point, func(t *testing.T) {
			s, store, _ := authorityBoundaryFixture(t)
			injected := errors.New(point)
			called := false
			err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(got string) error {
				if got == point {
					return injected
				}
				return nil
			}, func(*authorityLease) error { called = true; return nil })
			if !errors.Is(err, injected) || called {
				t.Fatalf("sync failure granted callback: called=%v err=%v", called, err)
			}
			if _, err := os.Lstat(store.lockPath()); !os.IsNotExist(err) {
				t.Fatalf("sync failure left own lock: %v", err)
			}
		})
	}
}

func TestAuthorityLockedBoundaryReadFailureIsNotRetried(t *testing.T) {
	s, store, _ := authorityBoundaryFixture(t)
	injected := errors.New("one-shot ACL failure")
	failed, called := false, false
	err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(point string) error {
		if point == "acl-query" && !failed {
			failed = true
			return injected
		}
		return nil
	}, func(*authorityLease) error { called = true; return nil })
	if !failed || called || !errors.Is(err, injected) {
		t.Fatalf("protection failure was hidden: failed=%v called=%v err=%v", failed, called, err)
	}
	if _, err := os.Stat(store.dir); !os.IsNotExist(err) {
		t.Fatalf("failed protected read created store: %v", err)
	}
}

func TestAuthorityLockedBoundaryAcquisitionFailures(t *testing.T) {
	for _, point := range []string{"lock-write", "lock-sync", "lock-close", "lock-dir-sync"} {
		t.Run(point, func(t *testing.T) {
			s, store, _ := authorityBoundaryFixture(t)
			injected := errors.New(point)
			called := false
			err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(got string) error {
				if got == point {
					return injected
				}
				return nil
			}, func(*authorityLease) error { called = true; return nil })
			if !errors.Is(err, injected) || called {
				t.Fatalf("acquisition failure granted: called=%v err=%v", called, err)
			}
			if _, err := os.Lstat(store.lockPath()); !os.IsNotExist(err) {
				t.Fatalf("failed acquisition left live own lock: %v", err)
			}
		})
	}
}

func TestAuthorityLockedBoundaryAcquisitionPreservesReplacement(t *testing.T) {
	s, store, _ := authorityBoundaryFixture(t)
	injected := errors.New("replace during sync failure")
	err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(point string) error {
		if point != "lock-sync" {
			return nil
		}
		if err := os.Rename(store.lockPath(), store.lockPath()+".own"); err != nil {
			return err
		}
		if err := os.WriteFile(store.lockPath(), []byte("foreign replacement"), 0644); err != nil {
			return err
		}
		return injected
	}, func(*authorityLease) error { t.Error("failed acquisition invoked callback"); return nil })
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	data, readErr := os.ReadFile(store.lockPath())
	if readErr != nil || string(data) != "foreign replacement" {
		t.Fatalf("replacement lost: %q %v", data, readErr)
	}
}

func TestAuthorityLockedBoundaryAllErrorsPreserved(t *testing.T) {
	s, _, _ := authorityBoundaryFixture(t)
	callbackErr, closeErr, releaseErr := errors.New("callback failure"), errors.New("close failure"), errors.New("release failure")
	inside := false
	err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(point string) error {
		if inside && point == "close" {
			return closeErr
		}
		if point == "release-dir-sync" {
			return releaseErr
		}
		return nil
	}, func(*authorityLease) error { inside = true; return callbackErr })
	for _, want := range []error{callbackErr, closeErr, releaseErr} {
		if !errors.Is(err, want) {
			t.Fatalf("lost error %v: %v", want, err)
		}
	}
}

func TestAuthorityLockedBoundaryLeaseLifetime(t *testing.T) {
	s, _, _ := authorityBoundaryFixture(t)
	err := s.withAuthorityMutation(context.Background(), "task-manager", func(lease *authorityLease) error {
		lease.valid = false
		if err := lease.validate(); !errors.Is(err, errAuthorityLeaseExpired) {
			t.Fatalf("expired lease accepted with open descriptor: %v", err)
		}
		lease.valid = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityLockedBoundaryFreshConfigAfterSync(t *testing.T) {
	s, _, path := authorityBoundaryFixture(t)
	changed, called := false, false
	err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(point string) error {
		if point == "config-sync" && !changed {
			changed = true
			return os.WriteFile(path, []byte(strings.Replace(canonicalTestProfile, "writer: none", "writer: ce", 1)), 0600)
		}
		return nil
	}, func(*authorityLease) error { called = true; return nil })
	if !changed || called || err == nil {
		t.Fatalf("config change granted: changed=%v called=%v err=%v", changed, called, err)
	}
}

func TestAuthorityLockedBoundaryLeaseDetectsDrift(t *testing.T) {
	for _, kind := range []string{"permissions", "same-size-bytes"} {
		t.Run(kind, func(t *testing.T) {
			s, _, path := authorityBoundaryFixture(t)
			err := s.withAuthorityMutation(context.Background(), "task-manager", func(lease *authorityLease) error {
				if kind == "permissions" {
					if err := os.Chmod(path, 0644); err != nil {
						return err
					}
					defer func() {
						if err := os.Chmod(path, 0600); err != nil {
							t.Error(err)
						}
					}()
				} else {
					data := strings.Replace(string(lease.config.raw), "task-manager", "task-managEr", 1)
					if err := os.WriteFile(path, []byte(data), 0600); err != nil {
						return err
					}
				}
				if err := lease.validate(); err == nil {
					t.Fatal("changed config accepted by lease")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthorityLockedBoundaryAlreadyLockedOwnership(t *testing.T) {
	s, ownStore, _ := authorityBoundaryFixture(t)
	for _, foreign := range []bool{false, true} {
		store := ownStore
		if foreign {
			store = newStateStore(t.TempDir())
		}
		lock, err := store.lockOwned()
		if err != nil {
			t.Fatal(err)
		}
		called := false
		err = s.withLockedCanonicalAuthority(context.Background(), lock, "task-manager", func(lease *authorityLease) error { called = true; return lease.validate() })
		if foreign && (err == nil || called) {
			t.Fatalf("foreign lock granted: called=%v err=%v", called, err)
		}
		if !foreign && (err != nil || !called) {
			t.Fatalf("owned lock refused: called=%v err=%v", called, err)
		}
		if err := lock.validate(); err != nil {
			t.Fatalf("already-locked validator released caller lock: %v", err)
		}
		if err := lock.release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuthorityLockedBoundarySymlinkCallerRefused(t *testing.T) {
	s, _, _ := authorityBoundaryFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(s.root, alias); err != nil {
		t.Fatal(err)
	}
	called := false
	err := NewService(alias, NewExecutor()).withCanonicalConfig(context.Background(), nil, func(canonicalConfigHandle) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("symlink caller accepted: called=%v err=%v", called, err)
	}
}

func TestAuthorityLockedBoundarySymlinkLockPreserved(t *testing.T) {
	s, store, _ := authorityBoundaryFixture(t)
	err := s.withAuthorityMutation(context.Background(), "task-manager", func(lease *authorityLease) error {
		if err := os.Rename(store.lockPath(), store.lockPath()+".original"); err != nil {
			return err
		}
		if err := os.Symlink(store.lockPath()+".original", store.lockPath()); err != nil {
			return err
		}
		if err := lease.validate(); err == nil {
			t.Error("symlink to owned inode accepted as held lock")
		}
		return nil
	})
	if err == nil {
		t.Fatal("symlink lock release reported success")
	}
	st, err := os.Lstat(store.lockPath())
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("replacement symlink removed: %v", err)
	}
}

func TestAuthorityLockedBoundaryReleaseCheckWindow(t *testing.T) {
	s, store, _ := authorityBoundaryFixture(t)
	var replaced []byte
	err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(point string) error {
		if point != "release-stat" {
			return nil
		}
		var err error
		replaced, err = os.ReadFile(store.lockPath())
		if err != nil {
			return err
		}
		if err := os.Rename(store.lockPath(), store.lockPath()+".own"); err != nil {
			return err
		}
		return os.WriteFile(store.lockPath(), replaced, 0644)
	}, func(*authorityLease) error { return nil })
	if err == nil {
		t.Fatal("replacement in release check/delete window accepted")
	}
	data, readErr := os.ReadFile(store.lockPath())
	if readErr != nil || string(data) != string(replaced) {
		t.Fatalf("foreign replacement deleted: %q %v", data, readErr)
	}
}

func TestAuthorityLockedBoundaryReleaseStagingWindow(t *testing.T) {
	s, store, _ := authorityBoundaryFixture(t)
	err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(point string) error {
		if point == "lock-retired" {
			return os.WriteFile(store.lockPath(), []byte("new owner"), 0644)
		}
		return nil
	}, func(*authorityLease) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.lockPath())
	if err != nil || string(data) != "new owner" {
		t.Fatalf("post-claim replacement deleted: %q %v", data, err)
	}
}

func TestAuthorityLockedBoundaryReleaseRetainsConflict(t *testing.T) {
	s, store, _ := authorityBoundaryFixture(t)
	err := s.withAuthorityMutationHook(context.Background(), "task-manager", func(point string) error {
		if point == "release-stat" {
			if err := os.Rename(store.lockPath(), store.lockPath()+".own"); err != nil {
				return err
			}
			return os.WriteFile(store.lockPath(), []byte("first foreign owner"), 0644)
		}
		if point == "lock-retired" {
			return os.WriteFile(store.lockPath(), []byte("second foreign owner"), 0644)
		}
		return nil
	}, func(*authorityLease) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "residual retained at") {
		t.Fatalf("missing residual evidence: %v", err)
	}
	data, readErr := os.ReadFile(store.lockPath())
	if readErr != nil || string(data) != "second foreign owner" {
		t.Fatalf("restore overwrote owner: %q %v", data, readErr)
	}
	residuals, globErr := filepath.Glob(filepath.Join(store.dir, ".mutation.lock.release-*"))
	if globErr != nil || len(residuals) != 1 {
		t.Fatalf("missing retained foreign file: %v %v", residuals, globErr)
	}
	data, readErr = os.ReadFile(residuals[0])
	if readErr != nil || string(data) != "first foreign owner" {
		t.Fatalf("retained content lost: %q %v", data, readErr)
	}
}
