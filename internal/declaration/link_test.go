package declaration

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/executiontiming"
)

func TestSeparateLinkWire(t *testing.T) {
	b := fixture(t, "link")
	l, err := DecodeLink(b)
	if err != nil {
		t.Fatal(err)
	}
	if l.Invocation == nil || l.Invocation.RunID != "example-attempt-1" {
		t.Fatal(l)
	}
	if _, err := Decode(b); !errors.Is(err, ErrUnsupported) {
		t.Fatal("link downcast to declaration", err)
	}
	if _, err := DecodeLink(fixture(t, "timing-v1")); !errors.Is(err, ErrUnsupported) {
		t.Fatal("timing downcast to link", err)
	}
	if _, err := executiontiming.ParseContext(b); err == nil {
		t.Fatal("old evidence reader accepted new envelope")
	}
	var old executiontiming.Record
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	if executiontiming.Validate(old) == nil {
		t.Fatal("old timing validator accepted new envelope")
	}
	for name, wire := range map[string]string{
		"duplicate":          strings.Replace(string(b), `"event_id":`, `"event_id":"other","event_id":`, 1),
		"unknown":            strings.Replace(string(b), `"schema":`, `"raw_topic":"secret","schema":`, 1),
		"null":               strings.Replace(string(b), `"invocation": {"schema": "task-manager.execution-timing/v1", "run_id": "example-attempt-1"}`, `"invocation":null`, 1),
		"missing":            strings.Replace(string(b), `"declaration_id": "declaration-example-1",`, "", 1),
		"type":               strings.Replace(string(b), `"run_id": "example-attempt-1"`, `"run_id":42`, 1),
		"bounds":             strings.Replace(string(b), `"event-example-1"`, `"`+strings.Repeat("x", 257)+`"`, 1),
		"unknown-invocation": strings.Replace(string(b), executiontiming.SchemaV1, "task-manager.execution-timing/v2", 1),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeLink([]byte(wire))
			if err == nil || !reflect.DeepEqual(got, Link{}) {
				t.Fatal("invalid link accepted", err)
			}
		})
	}
	other := l
	if s, _ := LinkRetransmission(l, other); s != "duplicate" {
		t.Fatal(s)
	}
	other.OccurredAt = "2026-01-01T00:00:04Z"
	if s, _ := LinkRetransmission(l, other); s != "conflict" {
		t.Fatal(s)
	}
	other = l
	other.Invocation = nil
	if s, _ := LinkRetransmission(l, other); s != "conflict" {
		t.Fatal("optional presence lost")
	}
}

func TestIsolatedSessionAndGenerationFixtures(t *testing.T) {
	r := example(t)
	second, err := Decode(fixture(t, "concurrent-session"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Runtime.SessionID == nil || *second.Runtime.SessionID == *r.Runtime.SessionID {
		t.Fatal("concurrent native sessions merged")
	}
	changed, err := Decode(fixture(t, "different-generation"))
	if err != nil {
		t.Fatal(err)
	}
	if a := Assess(changed, r.Repository, &r.Execution.Snapshot, "ACTIVE"); a.Current != "different-generation" {
		t.Fatal(a)
	}
	if _, err := Decode(fixture(t, "session-absent")); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing native ID accepted", err)
	}
	unavailable, err := Decode(fixture(t, "topic-only"))
	if err != nil || unavailable.Runtime.SessionID != nil {
		t.Fatal("unavailable ID synthesized", err)
	}
}
