package intentdoc

import (
	"math"
	"strings"
	"testing"
)

func TestIterationReferenceBoundaryFailures(t *testing.T) {
	intent := maintenanceDocument(t)
	cases := []struct {
		name     string
		previous func(*Iteration)
		next     func(*Iteration)
		cause    string
	}{
		{"stopped", func(d *Iteration) {
			d.Evaluation.Decision, d.Evaluation.StopReason = "stopped", "user-stop"
			d.Usage.NoProgressCount = 1
		}, nil, "terminal"},
		{"revise", func(d *Iteration) {
			d.Evaluation.Decision, d.Evaluation.StopReason = "revise", "revision-change"
			d.Usage.NoProgressCount = 1
		}, nil, "terminal"},
		{"intent revision", func(d *Iteration) { d.Intent.Revision = 2 }, nil, "intent or ordinal"},
		{"task decrease", nil, func(d *Iteration) { d.Usage.Tasks-- }, "cannot decrease"},
		{"elapsed decrease", nil, func(d *Iteration) { d.Usage.ElapsedSeconds-- }, "cannot decrease"},
		{"ordinal overflow", func(d *Iteration) {
			d.Ordinal = math.MaxUint32
			d.Previous = &ContextRef{"ITERATION-" + strings.Repeat("c", 32), 1, strings.Repeat("d", 64)}
		}, nil, "overflow"},
		{"count overflow", func(d *Iteration) {
			d.Ordinal = 2
			d.Previous = &ContextRef{"ITERATION-" + strings.Repeat("c", 32), 1, strings.Repeat("d", 64)}
			d.Usage.NoProgressCount = math.MaxUint32
		}, func(d *Iteration) {
			d.Evaluation.Decision = "continue"
		}, "overflow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := iterationFixture(t, intent)
			p.Usage.Tasks, p.Usage.ElapsedSeconds = 5, 7
			if tc.previous != nil {
				tc.previous(&p)
			}
			previous := parsedDocument(t, p)
			digest, _ := previous.Digest()
			next := iterationFixture(t, intent)
			next.ID = "ITERATION-" + strings.Repeat("b", 32)
			next.Ordinal = p.Ordinal + 1
			if p.Ordinal == math.MaxUint32 {
				next.Ordinal = math.MaxUint32
			}
			next.Previous = &ContextRef{previous.ID(), 1, digest}
			next.Usage = p.Usage
			if tc.next != nil {
				tc.next(&next)
			}
			record := parsedDocument(t, next)
			if err := ValidateIterationReferences(record, intent, &previous, nil); err == nil || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("expected %s: %v", tc.cause, err)
			}
		})
	}
}

func TestMaintenanceVersionBoundary(t *testing.T) {
	intent := maintenanceDocument(t)
	i, _ := intent.IntentSnapshot()
	i.SchemaVersion, i.Mode = 1, "completion"
	if _, err := Parse(encode(t, i)); err == nil {
		t.Fatal("v1 accepted maintenance fields")
	}
	i.SchemaVersion, i.Mode, i.Maintenance = 2, "maintenance", nil
	if _, err := Parse(encode(t, i)); err == nil {
		t.Fatal("maintenance missing contract accepted")
	}
	legacy, err := Parse([]byte(validIntent))
	if err != nil {
		t.Fatal(err)
	}
	iteration := parsedDocument(t, iterationFixture(t, legacy))
	if err := ValidateIterationReferences(iteration, legacy, nil, nil); err == nil || !strings.Contains(err.Error(), "maintenance intent") {
		t.Fatalf("iteration accepted completion intent: %v", err)
	}
}
