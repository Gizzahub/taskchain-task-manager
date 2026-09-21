package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

func claimBoard(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "claim me"}); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestClaimReleaseReplayAndReady(t *testing.T) {
	t.Parallel()
	root := claimBoard(t)
	req := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	record, err := Claim(root, req)
	if err != nil || record.Status != "held" {
		t.Fatalf("claim = %#v, %v", record, err)
	}
	ready, err := Ready(root)
	if err != nil || len(ready) != 0 {
		t.Fatalf("held ready = %#v, %v", ready, err)
	}
	cardPath := filepath.Join(root, "todo/TASK-1.md")
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, []byte(strings.Replace(string(raw), "status: pending", "status: done", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	replay, err := Claim(root, req)
	if err != nil || replay != record {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	if _, err := Claim(root, ClaimRequest{ID: "TASK-1", Owner: "other", Token: "abcdefabcdefabcdefabcdefabcdefab"}); err == nil {
		t.Fatal("second owner claimed held task")
	}
	released, err := Release(root, req)
	if err != nil || released.Status != "released" {
		t.Fatalf("release = %#v, %v", released, err)
	}
	releasedAgain, err := Release(root, req)
	if err != nil || releasedAgain != released {
		t.Fatalf("repeat release = %#v, %v", releasedAgain, err)
	}
	ready, err = Ready(root)
	if err != nil || len(ready) != 1 {
		t.Fatalf("released ready = %#v, %v", ready, err)
	}
	if _, err := Claim(root, req); err == nil {
		t.Fatal("released token reused")
	}
	if _, err := Release(root, ClaimRequest{ID: "TASK-1", Owner: "other", Token: testToken}); err == nil {
		t.Fatal("release accepted wrong owner")
	}
}

func TestClaimsLedgerSymlinkAndOversizeRejected(t *testing.T) {
	t.Parallel()
	root := claimBoard(t)
	target := filepath.Join(root, "real-ledger")
	if err := os.WriteFile(target, []byte(`{"schemaVersion":1,"records":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, claimsFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := List(root); err == nil {
		t.Fatal("symlink ledger accepted")
	}
	if err := os.Remove(filepath.Join(root, claimsFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, claimsFile), []byte(strings.Repeat("x", maxClaimsBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(root); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversize ledger error = %v", err)
	}
}

func TestConcurrentClaimHasOneWinner(t *testing.T) {
	t.Parallel()
	root := claimBoard(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := "0123456789abcdef0123456789abcde" + string(rune('0'+i))
			if _, err := Claim(root, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: token}); err == nil {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("claim winners = %d", winners)
	}
}

func TestClaimsCorruptLedgerFailsClosed(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"unknown field":      `{"schemaVersion":1,"records":[],"extra":1}`,
		"duplicate field":    `{"schemaVersion":1,"schemaVersion":1,"records":[]}`,
		"case variant field": `{"schemaVersion":1,"records":[{"id":"TASK-1","owner":"x","token":"0123456789abcdef0123456789abcdef","status":"held","Status":"released"}]}`,
		"trailing":           `{"schemaVersion":1,"records":[]} {}`,
		"bad status":         `{"schemaVersion":1,"records":[{"id":"TASK-1","owner":"x","token":"0123456789abcdef0123456789abcdef","status":"heldx"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := claimBoard(t)
			if err := os.WriteFile(filepath.Join(root, claimsFile), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := List(root); err == nil {
				t.Fatal("corrupt ledger accepted")
			}
			if _, err := Claim(root, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err == nil {
				t.Fatal("claim ignored corrupt ledger")
			}
		})
	}
}

func TestMissingHeldReferenceBlocksCreate(t *testing.T) {
	t.Parallel()
	root := claimBoard(t)
	raw := `{"schemaVersion":1,"records":[{"id":"TASK-99","owner":"worker","token":"0123456789abcdef0123456789abcdef","status":"held"}]}`
	if err := os.WriteFile(filepath.Join(root, claimsFile), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "blocked"}); err == nil || !strings.Contains(err.Error(), "missing task") {
		t.Fatalf("missing held reference error = %v", err)
	}
}

// TestClaimStatusVocabularyExhaustive exercises the real check that
// consumes ClaimStatus end to end: validateClaims's
// "record.Status != outputvocab.ClaimHeld && record.Status !=
// outputvocab.ClaimReleased" guard in claims.go, reached through loadClaims
// on every claim/release call. It drives both outputvocab.AllClaimStatuses()
// members through that guard via a raw on-disk ledger (mirroring
// TestMissingHeldReferenceBlocksCreate's approach, since the ledger's wire
// shape is what a real consumer would produce) and confirms a status
// outside the declared vocabulary is rejected rather than silently
// accepted.
func TestClaimStatusVocabularyExhaustive(t *testing.T) {
	t.Parallel()
	t.Run("held", func(t *testing.T) {
		t.Parallel()
		root := claimBoard(t)
		raw := `{"schemaVersion":1,"records":[{"id":"TASK-1","owner":"worker","token":"0123456789abcdef0123456789abcdef","status":"held"}]}`
		if err := os.WriteFile(filepath.Join(root, claimsFile), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		// Release (rather than a fresh Claim) proves the held-status ledger
		// loads and validates cleanly: it must pass validateClaims's guard
		// before Release can find and flip the matching record at all.
		if _, err := Release(root, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err != nil {
			t.Fatalf("held-status ledger rejected as invalid: %v", err)
		}
	})
	t.Run("released", func(t *testing.T) {
		t.Parallel()
		root := claimBoard(t)
		raw := `{"schemaVersion":1,"records":[{"id":"TASK-1","owner":"worker","token":"0123456789abcdef0123456789abcdef","status":"released"}]}`
		if err := os.WriteFile(filepath.Join(root, claimsFile), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Claim(root, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: "abcdef0123456789abcdef0123456789"}); err != nil {
			t.Fatalf("released-status ledger rejected as invalid: %v", err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		root := claimBoard(t)
		raw := `{"schemaVersion":1,"records":[{"id":"TASK-1","owner":"worker","token":"0123456789abcdef0123456789abcdef","status":"bogus-status"}]}`
		if err := os.WriteFile(filepath.Join(root, claimsFile), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Claim(root, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: "abcdef0123456789abcdef0123456789"})
		if err == nil || !strings.Contains(err.Error(), "invalid claim status") {
			t.Fatalf("validateClaims accepted a status outside the declared ClaimStatus vocabulary: err=%v", err)
		}
	})
}

func TestClaimValidationAndLedgerPreservedOnFailure(t *testing.T) {
	t.Parallel()
	root := claimBoard(t)
	before, err := os.ReadFile(filepath.Join(root, "todo/TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []ClaimRequest{
		{ID: "TASK-1", Owner: "worker", Token: "BAD"},
		{ID: "TASK-1", Owner: "worker\nunsafe", Token: testToken},
		{ID: "TASK-2", Owner: "worker", Token: testToken},
	} {
		if _, err := Claim(root, req); err == nil {
			t.Fatalf("invalid claim accepted: %#v", req)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, "todo/TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("claim changed card bytes")
	}
	if _, err := os.Stat(filepath.Join(root, claimsFile)); !os.IsNotExist(err) {
		t.Fatalf("ledger published on failure: %v", err)
	}
	if strings.Contains(string(after), "worker") {
		t.Fatal("claim data entered card")
	}
}
