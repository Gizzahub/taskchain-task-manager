package taskstore

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The pre-policy snapshot format for this synthetic board contains one card
// followed by the absent claims/journal markers, and no policy-file marker.
func legacyFixtureSnapshot(t *testing.T, dir string) string {
	t.Helper()
	path := "todo/TASK-1.md"
	raw, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d:%s:%o:%d:", len(path), path, info.Mode(), len(raw))
	h.Write(raw)
	fmt.Fprintf(h, "missing:%s;missing:%s;", claimsFile, transitionsFile)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestPrePolicySharedActivationCanResume(t *testing.T) {
	_, a, _ := sharedFixture(t)
	stop := errors.New("interrupted legacy initialization")
	_, err := enableSharedStep(a, false, func(at string) error {
		if at == "after-initializing" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	s, release, err := acquireShared(a, true)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	for i := range state.Participants {
		p := &state.Participants[i]
		// Deliberately install the previous version's independent hash.
		p.Snapshot = legacyFixtureSnapshot(t, filepath.Join(p.Root, state.BoardPath))
	}
	if err := publishSharedState(s.root, state, false); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableShared(a, true); err != nil {
		t.Fatalf("legacy resume: %v", err)
	}
}
