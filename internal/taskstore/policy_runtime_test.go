package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// bindRuntimeFixture is test-only preparation, not an activation protocol.
func bindRuntimeFixture(t *testing.T, dir string, d boardpolicy.Declaration) []byte {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	j, err := loadTransitions(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	p, digest := writeBoundPolicy(t, dir, d)
	j.SchemaVersion, j.PolicyDigest = 2, digest
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, transitionsFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	canonical, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestBoundParkingDiscoveryAndReady(t *testing.T) {
	dir := claimBoard(t)
	bindRuntimeFixture(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}, ZoneStatus: map[string]string{"manual": "done"}})
	if err := os.MkdirAll(filepath.Join(dir, "manual/done"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, id := range map[string]string{"manual/TASK-9.md": "TASK-9", "manual/done/TASK-8.md": "TASK-8"} {
		raw := "---\nid: " + id + "\nstatus: pending\n---\n# Parked\n"
		if err := os.WriteFile(filepath.Join(dir, path), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := List(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("list=%+v err=%v", entries, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Path, "manual/") && e.Card.Status != "done" {
			t.Fatalf("parking view=%+v", e)
		}
	}
	created, err := Create(dir, CreateRequest{Title: "dependent", DependsOn: []string{"TASK-9"}})
	if err != nil || created.Card.ID != "TASK-10" {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	ready, err := Ready(dir)
	if err != nil || len(ready) != 1 || ready[0].Card.ID != "TASK-1" {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
	if _, err := Claim(dir, ClaimRequest{ID: "TASK-9", Owner: "worker", Token: testToken}); err == nil {
		t.Fatal("parking was claimable as ready")
	}
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveIDs(dir, []string{"TASK-11"}, false); err != nil {
		t.Fatal(err)
	}
}

func TestBoundParkingResumeAndMoveOutOnly(t *testing.T) {
	dir := claimBoard(t)
	bindRuntimeFixture(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}, Transitions: []boardpolicy.Transition{{From: "manual", To: []string{"todo"}}}})
	if err := os.Mkdir(filepath.Join(dir, "manual"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo/TASK-1.md"), filepath.Join(dir, "manual/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	claim := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	if _, err := ClaimResume(dir, claim); err != nil {
		t.Fatal(err)
	}
	req := TransitionRequest{ID: claim.ID, Owner: claim.Owner, Token: claim.Token, RequestID: strings.Repeat("1", 32), From: "manual", To: "todo"}
	if _, err := Transition(dir, req); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	req.From, req.To, req.RequestID = "todo", "manual", strings.Repeat("2", 32)
	if _, err := Transition(dir, req); err == nil {
		t.Fatal("new parking target allowed")
	}
	req.To = "doing"
	if _, err := Transition(dir, req); err == nil {
		t.Fatal("default edge escaped explicit graph")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("rejected transition changed board")
	}
	if _, err := Release(dir, claim); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyCompletedReplaySurvivesBoundGraph(t *testing.T) {
	dir, req, _, _ := transitionFixture(t)
	want, err := Transition(dir, req)
	if err != nil {
		t.Fatal(err)
	}
	bindRuntimeFixture(t, dir, boardpolicy.Declaration{Transitions: []boardpolicy.Transition{{From: "doing", To: []string{"todo"}}}})
	before := boardBytes(t, dir)
	for _, replay := range []func(string, TransitionRequest) (TransitionResult, error){Transition, Recover} {
		got, err := replay(dir, req)
		if err != nil || got != want {
			t.Fatalf("replay=%+v err=%v", got, err)
		}
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("completed replay rewrote board")
	}
	req.From, req.To, req.RequestID = "doing", "todo", strings.Repeat("2", 32)
	if _, err := Transition(dir, req); err != nil {
		t.Fatal(err)
	}
}
