package taskstore

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// Synthetic journal promotion tests consumers, not revision admission. The
// production revision writer must bind its own durable all-participant plan.
func promoteHistoryFixture(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j, err := loadTransitions(r)
	if err != nil {
		t.Fatal(err)
	}
	p, err := policyForJournal(r, j)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	other, err := boardpolicy.New(boardpolicy.Declaration{Transitions: []boardpolicy.Transition{{From: "todo", To: []string{"done"}}}})
	if err != nil {
		t.Fatal(err)
	}
	old, err := other.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	j.SchemaVersion = 4
	j.PolicyHistory = map[string][]byte{bytesDigest(raw): raw, bytesDigest(old): old}
	if err := publishTransitionJournal(r, j); err != nil {
		t.Fatal(err)
	}
	return j.PolicyHistory
}

func assertHistoryFixture(t *testing.T, dir string, history map[string][]byte) {
	t.Helper()
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j, err := loadTransitions(r)
	if err != nil || j.SchemaVersion != 4 || !reflect.DeepEqual(j.PolicyHistory, history) {
		t.Fatalf("history lost: schema=%d err=%v", j.SchemaVersion, err)
	}
}

func TestPolicyHistorySharedMigrationAndCopiedJoin(t *testing.T) {
	repo, a, b := sharedFixture(t)
	raw := defaultPolicyBytes(t)
	histories := map[string]map[string][]byte{}
	for _, dir := range []string{a, b} {
		if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
			t.Fatal(err)
		}
		histories[dir] = promoteHistoryFixture(t, dir)
	}
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("synthetic history migration interruption")
	if _, err := activatePolicyWithStep(a, raw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
		if at == "board-0/after-policy-journal" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("interruption not reached: %v", err)
	}
	if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true, Resume: true}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a, b} {
		assertHistoryFixture(t, dir, histories[dir])
	}
	sharedGit(t, repo, "add", "tasks")
	sharedGit(t, repo, "commit", "-m", "synthetic history board")
	newRoot := filepath.Join(t.TempDir(), "history-copy")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	dir := filepath.Join(newRoot, "tasks")
	if _, err := List(dir); err == nil {
		t.Fatal("copied history bypassed explicit join")
	}
	if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	assertHistoryFixture(t, dir, histories[a])
}

func TestPolicyHistoryStillRequiresActivationReceipt(t *testing.T) {
	dir := claimBoard(t)
	if _, err := ActivatePolicy(dir, defaultPolicyBytes(t), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	promoteHistoryFixture(t, dir)
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(policyActivationFile); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Create(dir, CreateRequest{Title: "must reject"}); err == nil {
		t.Fatal("schema 4 admitted without activation receipt")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("rejection changed board")
	}
}

func TestPolicyHistorySurvivesTransitionAndReplay(t *testing.T) {
	dir, req, _, _ := transitionFixture(t)
	claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}
	if _, err := Release(dir, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(dir, defaultPolicyBytes(t), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	history := promoteHistoryFixture(t, dir)
	req.Token = strings.Repeat("d", 32)
	claim.Token = req.Token
	if _, err := Claim(dir, claim); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("synthetic transition interruption")
	if _, err := transitionWithStep(dir, req, func(at string) error {
		return stop
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if _, err := Recover(dir, req); err != nil {
		t.Fatal(err)
	}
	assertHistoryFixture(t, dir, history)
	before := boardBytes(t, dir)
	if _, err := Transition(dir, req); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("completed replay changed history board")
	}
}
