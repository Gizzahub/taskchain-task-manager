package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func snapshotFixture(t *testing.T) (string, *os.Root, transitionJournal) {
	t.Helper()
	dir := configuredFixture(t)
	if _, err := Create(dir, CreateRequest{ID: "TASK-1", Title: "Snapshot"}); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	j := transitionJournal{SchemaVersion: 1, BundleProtocol: 1, Records: []transitionRecord{}}
	if err := saveBundles(r, bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{}}, true); err != nil {
		r.Close()
		t.Fatal(err)
	}
	return dir, r, j
}

func TestPolicyActivationSnapshotStableAndCardMutationChanges(t *testing.T) {
	dir, r, journal := snapshotFixture(t)
	defer r.Close()
	first, err := policyActivationSnapshot(r, currentPolicy(), journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := policyActivationSnapshot(r, currentPolicy(), journal)
	if err != nil || first != second {
		t.Fatalf("unstable snapshot first=%s second=%s err=%v", first, second, err)
	}
	path := filepath.Join(dir, "todo", "TASK-1.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("\n<!-- changed -->\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := policyActivationSnapshot(r, currentPolicy(), journal)
	if err != nil || changed == first {
		t.Fatalf("card mutation did not change snapshot: %s %v", changed, err)
	}
}

func TestPolicyActivationSnapshotRejectsHeldAndPending(t *testing.T) {
	_, r, journal := snapshotFixture(t)
	defer r.Close()
	claims := claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{{ID: "TASK-1", Owner: "worker", Token: strings.Repeat("a", 32), Status: "held"}}}
	claimsRaw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile(claimsFile, claimsRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := policyActivationSnapshot(r, currentPolicy(), journal); err == nil || !strings.Contains(err.Error(), "held claim") {
		t.Fatalf("held claim accepted: %v", err)
	}
	pending := journal
	pending.Records = journalFixture(t).Records
	if _, err := policyActivationSnapshot(r, currentPolicy(), pending); err == nil || !strings.Contains(err.Error(), "pending transition") {
		t.Fatalf("pending transition accepted: %v", err)
	}
}

func TestPolicyActivationSnapshotMetadataMutationsChangeHash(t *testing.T) {
	mutate := func(t *testing.T, kind string) (string, string) {
		_, r, journal := snapshotFixture(t)
		defer r.Close()
		before, err := policyActivationSnapshot(r, currentPolicy(), journal)
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "ids":
			if err := r.Chmod(idsFile, 0o640); err != nil {
				t.Fatal(err)
			}
		case "claims":
			claimsRaw, err := json.Marshal(claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{{ID: "TASK-1", Owner: "worker", Token: strings.Repeat("b", 32), Status: "released"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.WriteFile(claimsFile, claimsRaw, 0o600); err != nil {
				t.Fatal(err)
			}
		case "bundle":
			if err := saveBundles(r, bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("c", 32), Records: []bundleRecord{}}, false); err != nil {
				t.Fatal(err)
			}
		}
		after, err := policyActivationSnapshot(r, currentPolicy(), journal)
		if err != nil {
			t.Fatal(err)
		}
		return before, after
	}
	for _, kind := range []string{"ids", "claims", "bundle"} {
		t.Run(kind, func(t *testing.T) {
			before, after := mutate(t, kind)
			if bytes.Equal([]byte(before), []byte(after)) {
				t.Fatal("metadata mutation did not change snapshot")
			}
		})
	}
}

func TestPolicyActivationSnapshotAdmitsDeclaredParkingZone(t *testing.T) {
	dir, r, journal := snapshotFixture(t)
	defer r.Close()
	if err := os.Mkdir(filepath.Join(dir, "parking"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo", "TASK-1.md"), filepath.Join(dir, "parking", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	policy, err := boardpolicy.New(boardpolicy.Declaration{Zones: []string{"parking"}, ZoneStatus: map[string]string{"parking": "cancelled"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policyActivationSnapshot(r, policy, journal); err != nil {
		t.Fatalf("declared parking zone rejected: %v", err)
	}
}

func TestPolicyActivationSnapshotExcludesTransactionOwnedFiles(t *testing.T) {
	_, r, journal := snapshotFixture(t)
	defer r.Close()
	before, err := policyActivationSnapshot(r, currentPolicy(), journal)
	if err != nil {
		t.Fatal(err)
	}
	// Recovery supplies the validated journal, so pending publication artifacts
	// must not send this helper back through ordinary admission or affect its hash.
	for _, name := range []string{transitionsFile, policyFile, policyActivationFile} {
		if err := r.WriteFile(name, []byte("interrupted publication fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	after, err := policyActivationSnapshot(r, currentPolicy(), journal)
	if err != nil || before != after {
		t.Fatalf("transaction files changed snapshot: %v", err)
	}
}
