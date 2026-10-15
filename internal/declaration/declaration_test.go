package declaration

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/executiontiming"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func example(t *testing.T) Record {
	t.Helper()
	r, err := Decode(fixture(t, "bound"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func ptr(s string) *string { return &s }

func TestStrictWire(t *testing.T) {
	base := string(fixture(t, "bound"))
	cases := map[string]string{
		"duplicate-key":      strings.Replace(base, `"id": "declaration-example-1"`, `"id":"other","id":"declaration-example-1"`, 1),
		"nested-duplicate":   strings.Replace(base, `"actor": "example-agent"`, `"actor":"x","actor":"example-agent"`, 1),
		"unknown-key":        strings.Replace(base, `"schema":`, `"secret-raw-key":"secret-raw-value","schema":`, 1),
		"case-alias":         strings.Replace(base, `"startedAt":`, `"startedat":`, 1),
		"null-ref":           strings.Replace(base, `"scope":`, `"predecessor":null,"scope":`, 1),
		"missing-purpose":    strings.Replace(base, `"purpose": "delivery",`, "", 1),
		"partial-ref":        strings.Replace(base, `"worktree": "/synthetic/worktrees/example-task",`, "", 1),
		"missing-start":      strings.Replace(base, `"startedAt": "2026-01-01T00:00:00Z"`, `"updatedAt":"2026-01-01T00:00:00Z"`, 1),
		"empty-ref":          strings.Replace(base, `"snapshot": {`, `"snapshot": {},"extra": {`, 1),
		"wrong-type":         strings.Replace(base, `"id": "declaration-example-1"`, `"id":42`, 1),
		"empty-session":      strings.Replace(base, `"native-example-1"`, `""`, 1),
		"fake-session":       strings.Replace(base, `"provenance": "native"`, `"provenance":"actor-host-derived"`, 1),
		"unpaired-surrogate": strings.Replace(base, `"declaration-example-1"`, `"\ud800"`, 1),
		"control":            strings.Replace(base, `"declaration-example-1"`, `"secret\nraw"`, 1),
		"format-control":     strings.Replace(base, `"declaration-example-1"`, `"secret\u202eraw"`, 1),
		"absolute-scope":     strings.Replace(base, `"src/example"`, `"/private/example"`, 1),
		"traversal":          strings.Replace(base, `"src/example"`, `"../example"`, 1),
		"long-topic":         strings.Replace(base, `"subject": {"task": "example-task"}`, `"subject":{"topic":"`+strings.Repeat("x", 257)+`"}`, 1),
		"trailing":           base + ` {}`,
		"oversize":           strings.Repeat(" ", MaxBytes) + base,
		"bad-utf8":           base + string([]byte{0xff}),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := Decode([]byte(b))
			if err == nil {
				t.Fatal("invalid wire accepted")
			}
			if !reflect.DeepEqual(r, Record{}) {
				t.Fatal("invalid input leaked partially decoded record")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error echoed private input")
			}
		})
	}
	for _, s := range []string{strings.Replace(base, SchemaV1, "task-manager.declaration/v2", 1), strings.Replace(base, LegacyScheme, "opaque-provider-id/v1", 1)} {
		if _, err := Decode([]byte(s)); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("expected unsupported, got %v", err)
		}
	}
}

func TestAdvisoryGenerationAndProvenance(t *testing.T) {
	r := example(t)
	current := r.Execution.Snapshot
	a := Assess(r, r.Repository, &current, taskruntime.StatusActive)
	if a.Current != "matching" || a.Provenance != "unknown" {
		t.Fatal(a)
	}
	for name, change := range map[string]func(*Snapshot){
		"started": func(s *Snapshot) { s.StartedAt = "2026-01-01T00:00:02Z" },
		"branch":  func(s *Snapshot) { s.Branch += "-other" },
		"source":  func(s *Snapshot) { s.Source = "other" },
		"owner":   func(s *Snapshot) { s.Owner.Host = "other" },
		"purpose": func(s *Snapshot) { s.Purpose = "" },
	} {
		t.Run(name, func(t *testing.T) {
			s := current
			change(&s)
			a := Assess(r, r.Repository, &s, taskruntime.StatusActive)
			if a.Current != "different-generation" || a.Provenance != "unknown" {
				t.Fatal(a)
			}
		})
	}
	if a := Assess(r, r.Repository, &current, taskruntime.StatusDone); a.Current != "terminal" || a.Provenance != "unknown" {
		t.Fatal(a)
	}
	if a := Assess(r, r.Repository, nil, ""); a.Current != "unavailable" {
		t.Fatal(a)
	}
	other := r.Repository
	other.CommonDir = "/synthetic/other/.git"
	other.CatalogAlias = ptr("example")
	if a := Assess(r, other, &current, taskruntime.StatusActive); a.Current != "unavailable" {
		t.Fatal("alias merged repositories", a)
	}
	u, err := Decode(fixture(t, "topic-only"))
	if err != nil {
		t.Fatal(err)
	}
	if a := Assess(u, u.Repository, nil, ""); a.Provenance != "declared-unbound" {
		t.Fatal(a)
	}
	second := example(t)
	second.Runtime.SessionID = ptr("native-example-2")
	if same, err := SamePayload(r, second); err != nil || same {
		t.Fatal("sessions collapsed")
	}
	// Self-reported authority is not a supported wire field.
	b := bytes.Replace(fixture(t, "bound"), []byte(`"schema":`), []byte(`"provenance":"bound-at-write","schema":`), 1)
	if _, err := Decode(b); err == nil {
		t.Fatal("self-asserted provenance accepted")
	}
}

func TestPayloadIdentity(t *testing.T) {
	a := example(t)
	var compact bytes.Buffer
	if err := json.Compact(&compact, fixture(t, "bound")); err != nil {
		t.Fatal(err)
	}
	b, err := Decode(compact.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if status, err := Retransmission(a, b); err != nil || status != "duplicate" {
		t.Fatal(status, err)
	}
	for name, change := range map[string]func(*Record){
		"time":        func(r *Record) { r.OccurredAt = "2026-01-01T00:00:02Z" },
		"presence":    func(r *Record) { r.Repository.CatalogAlias = ptr("example") },
		"scope-order": func(r *Record) { r.Scope.Writes = []string{"src/one", "src/two"} },
		"topic":       func(r *Record) { r.Subject.Topic = ptr("sanitized-short-topic"); r.Privacy.State = "redacted" },
	} {
		t.Run(name, func(t *testing.T) {
			b := example(t)
			change(&b)
			if s, err := Retransmission(a, b); err != nil || s != "conflict" {
				t.Fatal(s, err)
			}
		})
	}
	b = example(t)
	b.ID = "distinct-id"
	if s, _ := Retransmission(a, b); s != "distinct" {
		t.Fatal(s)
	}
	a.Scope.Writes = []string{"src/one", "src/two"}
	b = example(t)
	b.Scope.Writes = []string{"src/two", "src/one"}
	if s, _ := Retransmission(a, b); s != "conflict" {
		t.Fatal("array order lost")
	}
	a = example(t)
	b = example(t)
	a.Scope = nil
	b.Scope = &Scope{Reads: []Read{}, Writes: []string{}, Contracts: []string{}}
	if s, _ := Retransmission(a, b); s != "conflict" {
		t.Fatal("absent/empty scope collapsed")
	}
}

func TestLegacyEvidenceUnchanged(t *testing.T) {
	for _, name := range []string{"legacy-execution", "timing-v1", "evidence-v1"} {
		original := fixture(t, name)
		before := bytes.Clone(original)
		if _, err := Decode(original); err == nil {
			t.Fatal("legacy data mistaken for declaration")
		}
		if !bytes.Equal(before, original) {
			t.Fatal("input changed")
		}
	}
	var execution taskruntime.Execution
	if err := json.Unmarshal(fixture(t, "legacy-execution"), &execution); err != nil {
		t.Fatal(err)
	}
	r := example(t)
	s := r.Execution.Snapshot
	if execution.Task != s.Task || execution.Type != s.Type || string(execution.Purpose) != s.Purpose || execution.Source != s.Source || execution.Owner != s.Owner || execution.Branch != s.Branch || execution.Worktree != s.Worktree || execution.StartedAt.Format("2006-01-02T15:04:05Z07:00") != s.StartedAt {
		t.Fatal("legacy tuple mapping lost a field")
	}
	// updatedAt is deliberately excluded, with no ledger rewrite/defaulting.
	execution.UpdatedAt = execution.StartedAt
	var timing executiontiming.Record
	if err := json.Unmarshal(fixture(t, "timing-v1"), &timing); err != nil {
		t.Fatal(err)
	}
	if err := executiontiming.Validate(timing); err != nil {
		t.Fatal(err)
	}
	if _, err := executiontiming.ParseContext(fixture(t, "evidence-v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := executiontiming.ParseContext([]byte(`{"declaration_id":"example"}`)); err == nil {
		t.Fatal("old reader silently extended")
	}
}
