package taskstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaimPublishFailureCleansStageAndPreservesDestination(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A nonempty directory cannot be replaced by the staged regular file.
	// Exercise the publication failure directly, independent of input validation.
	if err := os.Mkdir(filepath.Join(dir, claimsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, claimsFile, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := saveClaims(r, claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{}}); err == nil {
		t.Fatal("rename onto nonempty directory succeeded")
	}
	raw, err := os.ReadFile(sentinel)
	if err != nil || string(raw) != "preserve me" {
		t.Fatalf("destination changed: %q %v", raw, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != claimsFile {
		t.Fatalf("staging leak: %v %v", entries, err)
	}
}

func TestClaimTokenBindingAndBoardLock(t *testing.T) {
	t.Parallel()
	root := claimBoard(t)
	if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "second"}); err != nil {
		t.Fatal(err)
	}
	req := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	if _, err := Claim(root, req); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, claimsFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wrong := req
	wrong.ID = "TASK-2"
	if _, err := Claim(root, wrong); err == nil {
		t.Fatal("token rebound to another task")
	}
	if _, err := Release(root, wrong); err == nil {
		t.Fatal("release accepted wrong task")
	}
	wrong = req
	wrong.Token = strings.Repeat("f", 32)
	if _, err := Release(root, wrong); err == nil {
		t.Fatal("release accepted wrong token")
	}
	if err := os.Mkdir(filepath.Join(root, ".task-manager.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(root, req); err == nil {
		t.Fatal("claim ignored board lock")
	}
	if _, err := Release(root, req); err == nil {
		t.Fatal("release ignored board lock")
	}
	if info, err := os.Stat(filepath.Join(root, ".task-manager.lock")); err != nil || !info.IsDir() {
		t.Fatalf("existing lock removed: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected operations changed ledger: %v", err)
	}
}

func TestClaimRejectsReservationOverflowEvenWhenHeldBytesFit(t *testing.T) {
	t.Parallel()
	root := claimBoard(t)
	if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "second"}); err != nil {
		t.Fatal(err)
	}
	first := ClaimRecord{ID: "TASK-1", Owner: "w", Token: testToken, Status: "held"}
	next := ClaimRecord{ID: "TASK-2", Owner: "w", Token: strings.Repeat("f", 32), Status: "held"}
	encode := func(ledger claimsLedger) []byte {
		t.Helper()
		raw, err := json.MarshalIndent(ledger, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return append(raw, '\n')
	}
	makeLedger := func(n int) claimsLedger {
		ledger := claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{first}}
		for i := 1; i <= n; i++ {
			ledger.Records = append(ledger.Records, ClaimRecord{ID: "TASK-99", Owner: "w", Token: fmt.Sprintf("%032x", i), Status: "released"})
		}
		ledger.Records = append(ledger.Records, next)
		return ledger
	}
	reservedSize := func(ledger claimsLedger) int {
		copyLedger := claimsLedger{SchemaVersion: 1, Records: append([]ClaimRecord(nil), ledger.Records...)}
		for i := range copyLedger.Records {
			copyLedger.Records[i].Status = "released"
		}
		return len(encode(copyLedger))
	}
	// A bounded binary search plus ASCII padding makes a precise fixture:
	// publishing both held records fits, but releasing both exceeds the cap by 2.
	low, high := 0, 10000
	if reservedSize(makeLedger(high)) <= maxClaimsBytes+2 {
		t.Fatal("fixture upper bound too small")
	}
	for low+1 < high {
		mid := (low + high) / 2
		if reservedSize(makeLedger(mid)) <= maxClaimsBytes+2 {
			low = mid
		} else {
			high = mid
		}
	}
	proposed := makeLedger(low)
	gap := maxClaimsBytes + 2 - reservedSize(proposed)
	for i := 1; gap > 0 && i <= 2; i++ {
		padding := min(gap, 127)
		proposed.Records[i].Owner += strings.Repeat("x", padding)
		gap -= padding
	}
	if gap != 0 || reservedSize(proposed) != maxClaimsBytes+2 || len(encode(proposed)) > maxClaimsBytes {
		t.Fatal("invalid exact-size fixture")
	}
	prior := claimsLedger{SchemaVersion: 1, Records: proposed.Records[:len(proposed.Records)-1]}
	if err := ensureReleaseCapacity(prior); err != nil {
		t.Fatalf("prior release must fit: %v", err)
	}
	raw := encode(prior)
	path := filepath.Join(root, claimsFile)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(root, ClaimRequest{ID: next.ID, Owner: next.Owner, Token: next.Token}); err == nil || !strings.Contains(err.Error(), "release capacity") {
		t.Fatalf("expected reservation rejection, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("rejected claim changed ledger: %v", err)
	}
	if _, err := Release(root, ClaimRequest{ID: first.ID, Owner: first.Owner, Token: first.Token}); err != nil {
		t.Fatalf("existing release failed: %v", err)
	}
}
