package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func writeBoundPolicy(t *testing.T, dir string, d boardpolicy.Declaration) (boardpolicy.Policy, string) {
	t.Helper()
	p, err := boardpolicy.New(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, policyFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return p, digest
}

func TestBoundCompletedParkingReceiptRequiresDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, digest := writeBoundPolicy(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}, Transitions: []boardpolicy.Transition{{From: "manual", To: []string{"todo"}}}})
	rec := transitionRecord{Kind: "completed", RequestID: strings.Repeat("a", 32), ID: "TASK-1", Owner: "worker", Token: strings.Repeat("b", 32), From: "manual", To: "todo", Source: "manual/TASK-1.md", Target: "todo/TASK-1.md", Mode: 0o644, Status: "completed"}
	raw, _ := json.Marshal(transitionJournal{SchemaVersion: 2, PolicyDigest: digest, Records: []transitionRecord{rec}})
	if err := os.WriteFile(filepath.Join(dir, transitionsFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := loadTransitions(r); err == nil {
		t.Fatal("parking receipt without digest is not a legacy operation")
	}
	rec.PolicyDigest = digest
	if err := publishTransitionJournal(r, transitionJournal{SchemaVersion: 2, PolicyDigest: digest, Records: []transitionRecord{rec}}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTransitions(r); err != nil {
		t.Fatalf("bound parking completed rejected: %v", err)
	}
}

func TestCompletedReceiptDigestMustMatchRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, digest := writeBoundPolicy(t, dir, boardpolicy.Declaration{})
	rec := transitionRecord{PolicyDigest: strings.Repeat("f", 64), Kind: "completed", RequestID: strings.Repeat("a", 32), ID: "TASK-1", Owner: "worker", Token: strings.Repeat("b", 32), From: "todo", To: "doing", Source: "todo/TASK-1.md", Target: "doing/TASK-1.md", Mode: 0o644, Status: "completed"}
	raw, _ := json.Marshal(transitionJournal{SchemaVersion: 2, PolicyDigest: digest, Records: []transitionRecord{rec}})
	_ = os.WriteFile(filepath.Join(dir, transitionsFile), raw, 0o600)
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := loadTransitions(r); err == nil {
		t.Fatal("mismatched completed digest accepted")
	}
}

func TestPublishRejectsMalformedPolicyBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, digest := writeBoundPolicy(t, dir, boardpolicy.Declaration{})
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	base := transitionRecord{Kind: "completed", RequestID: strings.Repeat("a", 32), ID: "TASK-1", Owner: "worker", Token: strings.Repeat("b", 32), From: "todo", To: "doing", Source: "todo/TASK-1.md", Target: "doing/TASK-1.md", Mode: 0o644, Status: "completed"}
	for name, j := range map[string]transitionJournal{"missing digest": {SchemaVersion: 2, Records: []transitionRecord{base}}, "bad digest": {SchemaVersion: 2, PolicyDigest: strings.Repeat("f", 64), Records: []transitionRecord{base}}, "legacy with digest": {SchemaVersion: 1, PolicyDigest: digest, Records: []transitionRecord{base}}} {
		t.Run(name, func(t *testing.T) {
			if err := publishTransitionJournal(r, j); err == nil {
				t.Fatal("malformed policy binding published")
			}
		})
	}
}
