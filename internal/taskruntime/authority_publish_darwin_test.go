//go:build darwin && cgo

package taskruntime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Synthetic controller minting exists only in tests on fresh test repositories.
func publicationInput(t *testing.T, root string, raw []byte) (*Service, authorityPublicationInput, *ownerTransitionScope) {
	t.Helper()
	s := NewService(root, NewExecutor())
	repo, err := s.canonicalRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := decodePublicationCurrent(raw)
	if err != nil {
		t.Fatal(err)
	}
	target := []byte(strings.Replace(strings.Replace(canonicalTestProfile, "writer: none", "writer: task-manager", 1), "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210", 1))
	in := authorityPublicationInput{repo, append([]byte(nil), raw...), d.Authority, target, "fedcba9876543210fedcba9876543210"}
	c := &authorityTransitionController{repository: repo, expectedRaw: append([]byte(nil), raw...), expectedAuthority: d.Authority, target: append([]byte(nil), target...), artifactIdentity: "synthetic-artifact", approved: true, frozen: true, drained: true,
		quiescence: func(context.Context) (authorityQuiescence, error) {
			return authorityQuiescence{true, true, true, true}, nil
		}}
	return s, in, &ownerTransitionScope{controller: c}
}

func TestAuthorityPublicationCAS(t *testing.T) {
	primary, linked, path := canonicalConfigFixture(t)
	s, in, scope := publicationInput(t, linked, []byte(canonicalTestProfile))
	r, err := s.publishCanonicalAuthority(context.Background(), in, scope)
	if err != nil || r.status != "published" || !bytes.Equal(r.observation.raw, in.targetRaw) {
		t.Fatalf("publish: %+v %v", r, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, in.targetRaw) {
		t.Fatalf("target: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(linked, ConfigFile)); !os.IsNotExist(err) {
		t.Fatal("linked config created")
	}
	if _, err := os.Stat(newStateStore(in.repository.primary.commonDir).lockPath()); !os.IsNotExist(err) {
		t.Fatalf("lock retained: %v", err)
	}
	_ = primary
}

func TestAuthorityPublicationScopeSourceBinding(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
	// Approval for A->T must not become approval for B->T even when the
	// caller supplies a correct CAS expectation for the changed current B.
	b := []byte(strings.Replace(canonicalTestProfile, "writer: none", "writer: ce", 1))
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	d, err := decodeCanonicalAuthority(b)
	if err != nil {
		t.Fatal(err)
	}
	in.expectedRaw = b
	in.expectedAuthority = d.Authority
	r, err := s.publishCanonicalAuthority(context.Background(), in, scope)
	if !errors.Is(err, errAuthorityPublicationRefused) || r.status != "refused" {
		t.Fatalf("rebound approval: %+v %v", r, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatal("source B lost", err)
	}
	if _, err := os.Stat(filepath.Join(in.repository.primary.commonDir, RuntimeStateDir)); !os.IsNotExist(err) {
		t.Fatalf("scope refusal created runtime state: %v", err)
	}
}

func TestAuthorityPublicationRetry(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
	if err := os.WriteFile(path, in.targetRaw, 0600); err != nil {
		t.Fatal(err)
	}
	// Confirmation succeeds despite stale old CAS, no drain and unresolved state;
	// it confirms exactly one approved target and never authorizes a transition.
	scope.confirmationOnly = true
	scope.controller.frozen = false
	scope.controller.drained = false
	scope.controller.quiescence = nil
	store := newStateStore(in.repository.primary.commonDir)
	if err := store.save([]Execution{{Task: "unfinished"}}); err != nil {
		t.Fatal(err)
	}
	r, err := s.publishCanonicalAuthority(context.Background(), in, scope)
	if err != nil || r.status != "already-published" {
		t.Fatalf("retry: %+v %v", r, err)
	}
	foreign := []byte(strings.Replace(string(in.targetRaw), "writer: task-manager", "writer: ce", 1))
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	r, err = s.publishCanonicalAuthority(context.Background(), in, scope)
	if !errors.Is(err, errAuthorityPublicationConflict) || r.status != "conflict" {
		t.Fatalf("confirmation transitioned: %+v %v", r, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, foreign) {
		t.Fatal("foreign bytes lost")
	}
}

func TestAuthorityPublicationLegacy(t *testing.T) {
	for _, schema := range []string{"2", "3", "4", "5", "6"} {
		t.Run(schema, func(t *testing.T) {
			primary, _, path := canonicalConfigFixture(t)
			raw := []byte("schema-version: " + schema + "\nrepository-id: synthetic\nworktree-roots:\n  synthetic-host: /synthetic/worktrees/synthetic\nintegration-provider: gz-git\nintegration-network-policy: no-fetch\n")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if schema == "4" || schema == "5" || schema == "6" {
				if _, _, err := decodePublicationCurrent(raw); err == nil {
					t.Fatal("invalid profile became legacy")
				}
				return
			}
			s, in, scope := publicationInput(t, primary, raw)
			r, err := s.publishCanonicalAuthority(context.Background(), in, scope)
			if err != nil || r.status != "published" {
				t.Fatalf("bootstrap: %+v %v", r, err)
			}
		})
	}
}

func TestAuthorityPublicationSyncFailures(t *testing.T) {
	for _, point := range []string{"publication-temp-created", "publication-temp-write", "publication-temp-sync", "publication-before-rename", "publication-renamed", "publication-directory-sync", "publication-config-sync", "publication-temp-close"} {
		t.Run(point, func(t *testing.T) {
			primary, _, path := canonicalConfigFixture(t)
			s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
			boom := errors.New("injected publication failure")
			r, err := s.publishCanonicalAuthorityHook(context.Background(), in, scope, func(p string) error {
				if p == point {
					return boom
				}
				return nil
			})
			if !errors.Is(err, boom) || r.status == "published" || r.status == "already-published" || len(r.observation.raw) != 0 {
				t.Fatalf("failure reported success: %+v %v", r, err)
			}
			got, _ := os.ReadFile(path)
			post := point == "publication-renamed" || point == "publication-directory-sync" || point == "publication-config-sync" || point == "publication-temp-close"
			want := in.expectedRaw
			if post {
				want = in.targetRaw
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("wrong phase bytes: %q", got)
			}
			entries, e := os.ReadDir(filepath.Dir(path))
			if e != nil {
				t.Fatal(e)
			}
			if len(entries) != 1 {
				t.Fatalf("temp leak: %+v", entries)
			}
			if !scope.controller.frozen {
				t.Fatal("failure released freeze")
			}
		})
	}
}

func TestAuthorityPublicationQuiescence(t *testing.T) {
	for _, kind := range []string{"nil-scope", "approval", "artifact", "freeze", "drain", "unknown-callers", "board-pending", "cleanup-pending", "execution", "finishing-live", "finishing-dead", "finishing-unknown", "malformed-ledger", "same-generation"} {
		t.Run(kind, func(t *testing.T) {
			primary, _, path := canonicalConfigFixture(t)
			s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
			store := newStateStore(in.repository.primary.commonDir)
			switch kind {
			case "nil-scope":
				scope = nil
			case "approval":
				scope.controller.approved = false
			case "artifact":
				scope.controller.artifactIdentity = ""
			case "freeze":
				scope.controller.frozen = false
			case "drain":
				scope.controller.drained = false
			case "unknown-callers", "board-pending", "cleanup-pending":
				scope.controller.quiescence = func(context.Context) (authorityQuiescence, error) {
					q := authorityQuiescence{true, true, true, true}
					switch kind {
					case "unknown-callers":
						q.callersKnown = false
					case "board-pending":
						q.boardClear = false
					case "cleanup-pending":
						q.cleanupClear = false
					}
					return q, nil
				}
			case "execution":
				if err := store.save([]Execution{{Task: "unfinished"}}); err != nil {
					t.Fatal(err)
				}
			case "finishing-live", "finishing-dead", "finishing-unknown":
				if err := store.init(); err != nil {
					t.Fatal(err)
				}
				pid := os.Getpid()
				if kind == "finishing-dead" {
					pid = 999999
				}
				if kind == "finishing-unknown" {
					pid = 0
				}
				if err := store.appendReceipt(Receipt{Task: "claim", Status: StatusFinishing, FinishPID: pid}); err != nil {
					t.Fatal(err)
				}
			case "malformed-ledger":
				if err := store.init(); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store.executionsPath(), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			case "same-generation":
				in.targetRaw = []byte(strings.Replace(string(in.targetRaw), in.targetGeneration, in.expectedAuthority.Generation, 1))
				in.targetGeneration = in.expectedAuthority.Generation
				scope.controller.target = append([]byte(nil), in.targetRaw...)
			}
			r, err := s.publishCanonicalAuthority(context.Background(), in, scope)
			if err == nil || r.status == "published" {
				t.Fatalf("quiescence %s accepted: %+v %v", kind, r, err)
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, in.expectedRaw) {
				t.Fatal("refusal changed config")
			}
		})
	}
}

func TestAuthorityPublicationReplacement(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
	foreign := []byte(strings.Replace(canonicalTestProfile, "writer: none", "writer: ce", 1))
	r, err := s.publishCanonicalAuthorityHook(context.Background(), in, scope, func(p string) error {
		if p == "publication-before-rename" {
			return os.WriteFile(path, foreign, 0600)
		}
		return nil
	})
	if err == nil || r.status == "published" {
		t.Fatalf("late foreign change accepted: %+v %v", r, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, foreign) {
		t.Fatal("foreign config overwritten")
	}
}

func TestAuthorityPublicationPostRenameIdentity(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
	r, err := s.publishCanonicalAuthorityHook(context.Background(), in, scope, func(p string) error {
		if p == "publication-renamed" {
			if err := os.Rename(path, path+".original-published"); err != nil {
				return err
			}
			return os.WriteFile(path, in.targetRaw, 0600)
		}
		return nil
	})
	if err == nil || r.status != "publication-unverified" || len(r.observation.raw) != 0 {
		t.Fatalf("different inode granted: %+v %v", r, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, in.targetRaw) {
		t.Fatal("replacement lost", err)
	}
}

func TestAuthorityPublicationCleanupPreservesReplacement(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
	var replaced string
	r, err := s.publishCanonicalAuthorityHook(context.Background(), in, scope, func(p string) error {
		if p == "publication-temp-sync" {
			return errors.New("before publication")
		}
		if p == "publication-before-cleanup" {
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				return err
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".task-runtime.publish-") {
					replaced = filepath.Join(filepath.Dir(path), e.Name())
					if err := os.Rename(replaced, replaced+".owned"); err != nil {
						return err
					}
					return os.WriteFile(replaced, []byte("foreign"), 0600)
				}
			}
			return errors.New("temp missing")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "temp retained") || r.status != "failed-before-publish" {
		t.Fatalf("cleanup: %+v %v", r, err)
	}
	got, err := os.ReadFile(replaced)
	if err != nil || string(got) != "foreign" {
		t.Fatal("foreign temp removed", err)
	}
}

func TestAuthorityPublicationConcurrent(t *testing.T) {
	primary, _, _ := canonicalConfigFixture(t)
	s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.publishCanonicalAuthority(context.Background(), in, scope) }()
	}
	wg.Wait()
	r, err := s.publishCanonicalAuthority(context.Background(), in, scope)
	if err != nil || r.status != "already-published" {
		t.Fatalf("concurrent retry: %+v %v", r, err)
	}
}

func TestAuthorityPublicationCrashChild(t *testing.T) {
	root := os.Getenv("TASKCHAIN_PUBLICATION_CRASH_ROOT")
	if root == "" {
		return
	}
	s, in, scope := publicationInput(t, root, []byte(canonicalTestProfile))
	_, err := s.publishCanonicalAuthorityHook(context.Background(), in, scope, func(p string) error {
		if p == "publication-renamed" {
			os.Exit(23)
		}
		return nil
	})
	t.Fatalf("crash not reached: %v", err)
}

func TestAuthorityPublicationCrashRetry(t *testing.T) {
	primary, _, path := canonicalConfigFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestAuthorityPublicationCrashChild$")
	cmd.Env = append(os.Environ(), "TASKCHAIN_PUBLICATION_CRASH_ROOT="+primary)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child: %s %v", out, err)
	}
	s, in, scope := publicationInput(t, primary, []byte(canonicalTestProfile))
	scope.confirmationOnly = true
	scope.controller.frozen = false
	scope.controller.drained = false
	scope.controller.quiescence = nil
	r, err := s.publishCanonicalAuthority(context.Background(), in, scope)
	if err != nil || r.status != "already-published" {
		t.Fatalf("crash retry: %+v %v", r, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, in.targetRaw) {
		t.Fatal("crash retry changed target")
	}
}
