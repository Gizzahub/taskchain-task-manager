package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextRegistryFailureBoundariesAndRetry(t *testing.T) {
	for _, phase := range []string{"after-stage", "after-link", "after-cleanup"} {
		t.Run(phase, func(t *testing.T) {
			board := filepath.Join(t.TempDir(), "tasks")
			if err := Init(board); err != nil {
				t.Fatal(err)
			}
			stop := errors.New("synthetic " + phase)
			_, err := registerContextWithStep(board, []byte(testContextIntent), func(at string) error {
				if at == phase {
					if phase == "after-cleanup" {
						if err := os.Mkdir(filepath.Join(board, ".task-manager.lock", "nested"), 0o700); err != nil {
							return err
						}
						return nil
					}
					return stop
				}
				return nil
			})
			if phase != "after-cleanup" && !errors.Is(err, stop) {
				t.Fatalf("phase=%s err=%v", phase, err)
			}
			path, _ := contextPath("intent", "INTENT-0123456789abcdef0123456789abcdef", 1)
			published := filepath.Join(board, filepath.FromSlash(path))
			_, statErr := os.Stat(published)
			if phase == "after-stage" && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("after-stage published=%v", statErr)
			}
			if phase != "after-stage" && statErr != nil {
				t.Fatalf("post-publish path missing: %v", statErr)
			}
			if phase == "after-cleanup" {
				if err == nil || !strings.Contains(err.Error(), "may already be registered") || !strings.Contains(err.Error(), "directory not empty") {
					t.Fatalf("cleanup warning missing: %v", err)
				}
				if err := os.Remove(filepath.Join(board, ".task-manager.lock", "nested")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(board, ".task-manager.lock")); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "after-stage" {
				matches, err := filepath.Glob(filepath.Join(board, ".task-manager-stage-*"))
				if err != nil || len(matches) != 0 {
					t.Fatalf("failed stage left artifacts: %v %v", matches, err)
				}
			}
			result, retryErr := RegisterContext(board, []byte(testContextIntent))
			if retryErr != nil || (phase == "after-stage" && result.Status != "registered") || (phase != "after-stage" && result.Status != "unchanged") {
				t.Fatalf("retry=%+v err=%v", result, retryErr)
			}
		})
	}
}

func TestContextRegistryRejectsIdentityAndPathInputs(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind, id string
		rev      uint32
	}{
		{"plan", "INTENT-0123456789abcdef0123456789abcdef", 1},
		{"intent", "INTENT-0123456789abcdef0123456789abcde!", 1},
		{"intent", "INTENT-0123456789abcdef0123456789abcdef", 0},
	} {
		if _, err := ShowContext(board, tc.kind, tc.id, tc.rev); err == nil {
			t.Fatalf("invalid identity accepted: %+v", tc)
		}
	}
	if _, err := RegisterContext(board, []byte(`{"schemaVersion":1,"kind":"intent","id":"INTENT-0123456789abcdef0123456789abcdef","revision":0}`)); err == nil {
		t.Fatal("invalid document revision accepted")
	}
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	if _, err := ShowContext(board, "intent", "INTENT-0123456789abcdef0123456789abcdef", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(board, contextDirectory, "intents")); err != nil {
		t.Fatal(err)
	}
}
