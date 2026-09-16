package taskstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaimCapacityReservesReleaseSpace(t *testing.T) {
	root := claimBoard(t)
	if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "second"}); err != nil {
		t.Fatal(err)
	}
	base := []ClaimRecord{{ID: "TASK-1", Owner: "w", Token: testToken, Status: "held"}}
	makeLedger := func(n int) (claimsLedger, []byte) {
		records := append([]ClaimRecord(nil), base...)
		for i := 1; i <= n; i++ {
			records = append(records, ClaimRecord{ID: "TASK-99", Owner: "w", Token: fmt.Sprintf("%032x", i), Status: "released"})
		}
		ledger := claimsLedger{SchemaVersion: 1, Records: records}
		raw, err := json.MarshalIndent(ledger, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return ledger, raw
	}
	allReleasedSize := func(n int) int {
		ledger, _ := makeLedger(n)
		for i := range ledger.Records {
			if ledger.Records[i].Status == "held" {
				ledger.Records[i].Status = "released"
			}
		}
		raw, err := json.MarshalIndent(ledger, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return len(raw) + 1
	}
	low, high := 0, 10000
	for low+1 < high {
		mid := (low + high) / 2
		if allReleasedSize(mid) <= maxClaimsBytes {
			low = mid
		} else {
			high = mid
		}
	}
	_, raw := makeLedger(low)
	if allReleasedSize(low) > maxClaimsBytes || allReleasedSize(low+1) <= maxClaimsBytes {
		t.Fatal("failed to construct capacity boundary")
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(root, claimsFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, claimsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(root, ClaimRequest{ID: "TASK-2", Owner: "w", Token: "abcdefabcdefabcdefabcdefabcdefab"}); err == nil || !strings.Contains(err.Error(), "release capacity") {
		t.Fatalf("capacity claim error = %v", err)
	}
	after, err := os.ReadFile(filepath.Join(root, claimsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("capacity rejection changed ledger")
	}
	released, err := Release(root, ClaimRequest{ID: "TASK-1", Owner: "w", Token: testToken})
	if err != nil || released.Status != "released" {
		t.Fatalf("reserved release = %#v, %v", released, err)
	}
}

func TestClaimReplayAfterLeavingTodo(t *testing.T) {
	root := claimBoard(t)
	req := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	claimed, err := Claim(root, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "doing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "todo/TASK-1.md"), filepath.Join(root, "doing/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	replay, err := Claim(root, req)
	if err != nil || replay != claimed {
		t.Fatalf("replay after state change = %#v, %v", replay, err)
	}
	if released, err := Release(root, req); err != nil || released.Status != "released" {
		t.Fatalf("release after state change = %#v, %v", released, err)
	}
}

func TestClaimLedgerRequiredAndMalformedFormsFailClosed(t *testing.T) {
	cases := map[string]string{
		"missing records":  `{"schemaVersion":1}`,
		"null records":     `{"schemaVersion":1,"records":null}`,
		"missing version":  `{"records":[]}`,
		"wrong version":    `{"schemaVersion":2,"records":[]}`,
		"truncated":        `{"schemaVersion":1,"records":[`,
		"empty":            "",
		"duplicate status": `{"schemaVersion":1,"records":[{"id":"TASK-1","owner":"w","token":"0123456789abcdef0123456789abcdef","status":"held","status":"released"}]}`,
	}
	cases["invalid UTF-8"] = string([]byte(`{"schemaVersion":1,"records":[{"id":"TASK-1","owner":"`)) + string([]byte{0xff}) + `","token":"0123456789abcdef0123456789abcdef","status":"held"}]}`
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			root := claimBoard(t)
			if err := os.WriteFile(filepath.Join(root, claimsFile), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Ready(root); err == nil {
				t.Fatal("Ready accepted malformed ledger")
			}
			if _, err := List(root); err == nil {
				t.Fatal("List accepted malformed ledger")
			}
			if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "new"}); err == nil {
				t.Fatal("Create accepted malformed ledger")
			}
			if _, err := Claim(root, ClaimRequest{ID: "TASK-1", Owner: "w", Token: testToken}); err == nil {
				t.Fatal("Claim accepted malformed ledger")
			}
			after, err := os.ReadFile(filepath.Join(root, claimsFile))
			if err != nil || string(after) != raw {
				t.Fatalf("malformed ledger was changed: %v", err)
			}
		})
	}
}

func TestClaimOwnerBoundaryValidation(t *testing.T) {
	root := claimBoard(t)
	for _, owner := range []string{" ", "\t", string([]byte{0xff})} {
		if _, err := Claim(root, ClaimRequest{ID: "TASK-1", Owner: owner, Token: testToken}); err == nil {
			t.Fatalf("invalid owner accepted: %q", owner)
		}
	}
}
