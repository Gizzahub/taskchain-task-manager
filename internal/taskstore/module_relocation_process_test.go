package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moduleTaskRelocationRequest(t *testing.T, board, source, target, id string) RelocationRequest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(board, filepath.FromSlash(source)))
	if err != nil {
		t.Fatal(err)
	}
	return RelocationRequest{
		ID:             id,
		Owner:          "worker",
		RequestID:      strings.Repeat("e", 32),
		Source:         source,
		Target:         target,
		ExpectedSHA256: bytesDigest(raw),
	}
}

func assertModuleRelocationSourceListed(t *testing.T, board, source string) {
	t.Helper()
	entries, err := List(board)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path == source {
			return
		}
	}
	t.Fatalf("module relocation source not discovered: %s", source)
}

func moduleWorkflowRelocationFixture(t *testing.T) (string, string, RelocationRequest) {
	t.Helper()
	board := moduleRelocationBoard(t)
	entry, err := Create(board, CreateRequest{Title: "process relocation", Module: "backend", Category: "auth/api"})
	if err != nil {
		t.Fatal(err)
	}
	source := entry.Path
	target := "backend/plan/auth/api/" + filepath.Base(filepath.FromSlash(source))
	assertModuleRelocationSourceListed(t, board, source)
	return board, source, moduleTaskRelocationRequest(t, board, source, target, entry.Card.ID)
}

func TestModuleRelocationProcessKillAndContention(t *testing.T) {
	for _, point := range []string{"after-target", "after-source"} {
		t.Run("local/"+point, func(t *testing.T) {
			board, _, req := moduleWorkflowRelocationFixture(t)
			runRelocationCrash(t, board, board, "", point, req)
		})
	}
}

func TestModuleRelocationSharedProcessKillAndContention(t *testing.T) {
	for _, point := range []string{"after-relocation-common-pending", "after-target", "after-source"} {
		t.Run(point, func(t *testing.T) {
			_, board, other := sharedFixture(t)
			if _, err := EnableShared(board, false); err != nil {
				t.Fatal(err)
			}
			if _, err := ActivatePolicy(board, moduleRelocationRaw(t), PolicyActivationOptions{AllWorktrees: true, AdoptModules: true}); err != nil {
				t.Fatal(err)
			}
			entry, err := Create(board, CreateRequest{Title: "shared process relocation", Module: "backend", Category: "auth/api"})
			if err != nil {
				t.Fatal(err)
			}
			source := entry.Path
			target := "backend/plan/auth/api/" + filepath.Base(filepath.FromSlash(source))
			assertModuleRelocationSourceListed(t, board, source)
			req := moduleTaskRelocationRequest(t, board, source, target, entry.Card.ID)
			s, release, err := acquireShared(board, false)
			if err != nil {
				t.Fatal(err)
			}
			commonLock := filepath.Join(s.root.Name(), ".task-manager.lock")
			if err := release(); err != nil {
				t.Fatal(err)
			}
			runRelocationCrash(t, board, other, commonLock, point, req)
		})
	}
}
