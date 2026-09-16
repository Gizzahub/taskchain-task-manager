package intentdoc

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func maintenanceDocument(t *testing.T) Document {
	t.Helper()
	var d Intent
	if err := json.Unmarshal([]byte(validIntent), &d); err != nil {
		t.Fatal(err)
	}
	m := validMaintenance()
	d.SchemaVersion, d.Mode, d.Maintenance = 2, "maintenance", &m
	return parsedDocument(t, d)
}

func parsedDocument(t *testing.T, value any) Document {
	t.Helper()
	d, err := Parse(encode(t, value))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func iterationFixture(t *testing.T, intent Document) Iteration {
	t.Helper()
	digest, err := intent.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return Iteration{
		SchemaVersion: 2, Kind: "iteration", ID: "ITERATION-" + strings.Repeat("a", 32), Revision: 1,
		Intent: IntentRef{intent.ID(), intent.Revision(), digest}, Ordinal: 1,
		Trigger: IterationTrigger{Key: "manual-check", EvidenceRefs: []string{"synthetic-trigger"}},
		Usage:   IterationUsage{},
		Evaluation: IterationEvaluation{Actor: "observer", Progress: false,
			Criteria: []CriterionEvaluation{{Key: "verified", Result: "met", EvidenceRefs: []string{"synthetic-check"}}},
			Decision: "idle", StopReason: "none", Reason: "No work required", RemainingGaps: []string{}, EvidenceRefs: []string{}},
	}
}

func TestIterationRoundTripAndV1Preservation(t *testing.T) {
	legacy, err := Parse([]byte(validIntent))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := legacy.Canonical()
	if string(raw) != validIntent {
		t.Fatal("schema 1 canonical bytes changed")
	}
	intent := maintenanceDocument(t)
	d := iterationFixture(t, intent)
	record := parsedDocument(t, d)
	if err := ValidateIterationReferences(record, intent, nil, nil); err != nil {
		t.Fatal(err)
	}
	canonical, _ := record.Canonical()
	if bytes.Contains(canonical, []byte(`"batch"`)) || bytes.Contains(canonical, []byte(`"previous"`)) {
		t.Fatal("absent references must be omitted")
	}
	round, err := Parse(canonical)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := round.Canonical()
	if !bytes.Equal(canonical, got) {
		t.Fatal("round trip changed canonical")
	}
	snapshot, _ := record.IterationSnapshot()
	snapshot.Evaluation.Criteria[0].EvidenceRefs[0] = "changed"
	again, _ := record.Canonical()
	if !bytes.Equal(canonical, again) {
		t.Fatal("mutable snapshot escaped into document")
	}
	d.Evaluation.Decision, d.Evaluation.StopReason = "stopped", "budget-exhausted"
	d.Usage = IterationUsage{Tasks: math.MaxUint32, ElapsedSeconds: math.MaxUint32, NoProgressCount: 1}
	if err := ValidateIterationReferences(parsedDocument(t, d), intent, nil, nil); err != nil {
		t.Fatalf("over-budget observation lost: %v", err)
	}
}

func TestIterationLocalInvalid(t *testing.T) {
	intent := maintenanceDocument(t)
	for name, mutate := range map[string]func(*Iteration){
		"schema":              func(d *Iteration) { d.SchemaVersion = 1 },
		"revision":            func(d *Iteration) { d.Revision = 2 },
		"ordinal":             func(d *Iteration) { d.Ordinal = 2 },
		"idle count":          func(d *Iteration) { d.Usage.NoProgressCount = 1 },
		"idle progress":       func(d *Iteration) { d.Evaluation.Progress = true },
		"achieved":            func(d *Iteration) { d.Evaluation.Decision = "achieved" },
		"stop reason":         func(d *Iteration) { d.Evaluation.StopReason = "user-stop" },
		"trigger evidence":    func(d *Iteration) { d.Trigger.EvidenceRefs = []string{} },
		"met evidence":        func(d *Iteration) { d.Evaluation.Criteria[0].EvidenceRefs = []string{} },
		"duplicate criterion": func(d *Iteration) { d.Evaluation.Criteria = append(d.Evaluation.Criteria, d.Evaluation.Criteria[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			d := iterationFixture(t, intent)
			mutate(&d)
			if _, err := Parse(encode(t, d)); err == nil {
				t.Fatal("invalid iteration accepted")
			}
		})
	}
	raw := encode(t, iterationFixture(t, intent))
	for _, changed := range [][]byte{
		bytes.Replace(raw, []byte(`"progress":false`), []byte(`"progress":"false"`), 1),
		bytes.Replace(raw, []byte(`"progress":false`), []byte(`"progress":null`), 1),
		bytes.Replace(raw, []byte(`"ordinal":1`), []byte(`"ordinal":1.0`), 1),
		bytes.Replace(raw, []byte(`"ordinal":1`), []byte(`"ordinal":4294967296`), 1),
	} {
		if bytes.Equal(raw, changed) {
			t.Fatal("mutation missed target")
		}
		if _, err := Parse(changed); err == nil {
			t.Fatal("invalid shape accepted")
		}
	}
}

func TestIterationPredecessorAndObservations(t *testing.T) {
	intent := maintenanceDocument(t)
	first := parsedDocument(t, iterationFixture(t, intent))
	digest, _ := first.Digest()
	next := iterationFixture(t, intent)
	next.ID, next.Ordinal = "ITERATION-"+strings.Repeat("b", 32), 2
	next.Previous = &ContextRef{first.ID(), first.Revision(), digest}
	if err := ValidateIterationReferences(parsedDocument(t, next), intent, &first, nil); err != nil {
		t.Fatalf("healthy idle counted as no progress: %v", err)
	}
	next.Evaluation.Decision, next.Evaluation.StopReason = "blocked", "permission-required"
	next.Usage.NoProgressCount = 1
	if err := ValidateIterationReferences(parsedDocument(t, next), intent, &first, nil); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Iteration){
		"wrong digest":      func(d *Iteration) { d.Previous.Digest = strings.Repeat("0", 64) },
		"wrong ordinal":     func(d *Iteration) { d.Ordinal = 3 },
		"unknown trigger":   func(d *Iteration) { d.Trigger.Key = "unknown" },
		"unknown criterion": func(d *Iteration) { d.Evaluation.Criteria[0].Key = "unknown" },
		"count mismatch":    func(d *Iteration) { d.Usage.NoProgressCount = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := parsedDocument(t, next)
			d, _ := copy.IterationSnapshot()
			mutate(&d)
			if err := ValidateIterationReferences(parsedDocument(t, d), intent, &first, nil); err == nil {
				t.Fatal("invalid relation accepted")
			}
		})
	}
}
