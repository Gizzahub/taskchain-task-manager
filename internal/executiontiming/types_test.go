package executiontiming

import (
	"strings"
	"testing"
	"time"
)

func validRecord() Record {
	d := int64(3)
	return Record{Schema: SchemaV1, RunID: "attempt-1", StartedAt: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 13, 0, 0, 1, 0, time.UTC), Status: StatusSuccess, DurationMonotonicNS: &d, ClockDomainID: "process-1"}
}

func TestParseContextAllowlistAndRelations(t *testing.T) {
	context, err := ParseContext([]byte(`{"session_id":"s","tool_call_id":"c","relation_type":"launches","retry_of":"prior"}`))
	if err != nil || context.SessionID != "s" || context.ToolCallID != "c" {
		t.Fatalf("context %#v, err %v", context, err)
	}
	if _, err := ParseContext(nil); err != nil {
		t.Fatalf("empty context: %v", err)
	}
	for _, input := range []string{`{"unknown":"x"}`, `{"task_id":"a","task_id":"b"}`, `{"session_id":3}`, `{"session_id":null}`, `{"session_id":""}`, `{"relation_type":"launches","session_id":"s"}`, `{"relation_type":"retries"}`} {
		if _, err := ParseContext([]byte(input)); err == nil {
			t.Fatalf("ParseContext(%s) unexpectedly passed", input)
		}
	}
	if _, err := ParseContext([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}); err == nil {
		t.Fatal("invalid UTF-8 context unexpectedly passed")
	}
}

func TestValidatePreservesMonotonicTruthOverUTCOrdering(t *testing.T) {
	r := validRecord()
	r.FinishedAt = r.StartedAt.Add(-time.Hour) // wall-clock skew is not a monotonic failure.
	if err := Validate(r); err != nil {
		t.Fatalf("valid monotonic record rejected: %v", err)
	}
	negative := int64(-1)
	r.DurationMonotonicNS = &negative
	if err := Validate(r); err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("negative duration err %v", err)
	}
}

func TestValidateRejectsUnsafeIDsAndUnsupportedContext(t *testing.T) {
	for _, mutate := range []func(*Record){
		func(r *Record) { r.RunID = " a" },
		func(r *Record) { r.Status = "invented" },
		func(r *Record) { invalid := 256; r.ExitCode = &invalid },
		func(r *Record) { r.ClockDomainID = "" },
		func(r *Record) { r.Context.TaskID = "bad\nvalue" },
		func(r *Record) { r.StartedAt = r.StartedAt.In(time.FixedZone("local", 3600)) },
		func(r *Record) { r.FinishedAt = time.Time{} },
	} {
		r := validRecord()
		mutate(&r)
		if err := Validate(r); err == nil {
			t.Fatal("invalid record unexpectedly passed")
		}
	}
}
