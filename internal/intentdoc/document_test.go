package intentdoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const validIntent = `{"schemaVersion":1,"kind":"intent","id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"title":"Synthetic goal","outcome":"A verifiable outcome","mode":"completion","constraints":[],"nonGoals":[],"successCriteria":[{"key":"verified","text":"Observable result"}]}`

func batchFixture(t *testing.T) Batch {
	t.Helper()
	d, err := Parse([]byte(validIntent))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := d.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return Batch{SchemaVersion: 1, Kind: "batch", ID: "BATCH-0123456789abcdef0123456789abcdef", Revision: 1, Intent: IntentRef{d.ID(), d.Revision(), digest}, Gap: "Missing implementation", TaskIDs: []string{"TASK-001", "TASK-2"}, Constraints: []string{}, AuthorizationRefs: []string{}}
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCanonicalRoundTripAndIsolation(t *testing.T) {
	for _, raw := range [][]byte{[]byte(validIntent), encode(t, batchFixture(t))} {
		d, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := d.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		digest, err := d.Digest()
		if err != nil || digest != fmt.Sprintf("%x", sha256.Sum256(canonical)) {
			t.Fatalf("digest=%s err=%v", digest, err)
		}
		if d.ID() == "" || d.Revision() != 1 || d.Kind() == "" {
			t.Fatal("missing identity")
		}
		second, err := Parse(canonical)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := second.Canonical()
		if !bytes.Equal(canonical, again) {
			t.Fatal("canonical round trip changed bytes")
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, canonical, "", "  "); err != nil {
			t.Fatal(err)
		}
		third, err := Parse(pretty.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		otherDigest, _ := third.Digest()
		if digest != otherDigest {
			t.Fatal("formatting changed digest")
		}
		raw[0], canonical[0] = 'x', 'x'
		unchanged, _ := d.Canonical()
		if !bytes.Equal(unchanged, again) {
			t.Fatal("caller mutation escaped into document")
		}
	}
	if _, err := (Document{}).Canonical(); err == nil {
		t.Fatal("zero canonical accepted")
	}
	if _, err := (Document{}).Digest(); err == nil {
		t.Fatal("zero digest accepted")
	}
}

func TestIntentSemanticFailures(t *testing.T) {
	var valid Intent
	if err := json.Unmarshal([]byte(validIntent), &valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Intent){
		"schema":           func(d *Intent) { d.SchemaVersion = 2 },
		"zero revision":    func(d *Intent) { d.Revision = 0 },
		"numeric pilot ID": func(d *Intent) { d.ID = "INTENT-1" },
		"wrong kind ID":    func(d *Intent) { d.ID = strings.Replace(d.ID, "INTENT", "BATCH", 1) },
		"empty title":      func(d *Intent) { d.Title = "" },
		"padded title":     func(d *Intent) { d.Title = " leading" },
		"title newline":    func(d *Intent) { d.Title = "a\nb" },
		"long title":       func(d *Intent) { d.Title = strings.Repeat("x", 257) },
		"long outcome":     func(d *Intent) { d.Outcome = strings.Repeat("x", (16<<10)+1) },
		"unsupported mode": func(d *Intent) { d.Mode = "maintenance" },
		"null list":        func(d *Intent) { d.Constraints = nil },
		"control":          func(d *Intent) { d.Constraints = []string{"embedded\x00byte"} },
		"empty criterion":  func(d *Intent) { d.SuccessCriteria = []Criterion{} },
		"duplicate key":    func(d *Intent) { d.SuccessCriteria = []Criterion{{"same", "First"}, {"same", "Second"}} },
		"bad key":          func(d *Intent) { d.SuccessCriteria = []Criterion{{"../escape", "Text"}} },
		"too many":         func(d *Intent) { d.NonGoals = make([]string, 129) },
	} {
		t.Run(name, func(t *testing.T) {
			d := valid
			mutate(&d)
			if _, err := Parse(encode(t, d)); err == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
	valid.Revision = ^uint32(0)
	valid.Title = strings.Repeat("x", 256)
	valid.Outcome = strings.Repeat("x", 16<<10)
	if _, err := Parse(encode(t, valid)); err != nil {
		t.Fatalf("exact field limits: %v", err)
	}
}

func TestBatchEvaluationAndTaskIdentity(t *testing.T) {
	b := batchFixture(t)
	for name, mutate := range map[string]func(*Batch){
		"empty tasks":        func(d *Batch) { d.TaskIDs = []string{} },
		"alias duplicate":    func(d *Batch) { d.TaskIDs = []string{"TASK-1", "TASK-001"} },
		"kind link":          func(d *Batch) { d.TaskIDs = []string{"PLAN-1"} },
		"overflow":           func(d *Batch) { d.TaskIDs = []string{"TASK-18446744073709551616"} },
		"bad digest":         func(d *Batch) { d.Intent.Digest = strings.Repeat("G", 64) },
		"reference revision": func(d *Batch) { d.Intent.Revision = 0 },
		"empty gap":          func(d *Batch) { d.Gap = "" },
	} {
		t.Run(name, func(t *testing.T) {
			d := b
			mutate(&d)
			if _, err := Parse(encode(t, d)); err == nil {
				t.Fatal("invalid batch accepted")
			}
		})
	}
	evaluation := Evaluation{Actor: "reviewer", Intent: b.Intent, Decision: "achieved", Reason: "Submitted observation", RemainingGaps: []string{}, EvidenceRefs: []string{"evidence/result.md"}}
	b.Evaluation = &evaluation
	d, err := Parse(encode(t, b))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := d.Canonical()
	if !bytes.Contains(raw, []byte(`"TASK-001"`)) {
		t.Fatal("TASK spelling was rewritten")
	}
	for name, mutate := range map[string]func(*Evaluation){
		"changed reference": func(e *Evaluation) { e.Intent.Revision++ },
		"no actor":          func(e *Evaluation) { e.Actor = "" },
		"no reason":         func(e *Evaluation) { e.Reason = "" },
		"bad decision":      func(e *Evaluation) { e.Decision = "approved" },
		"remaining gap":     func(e *Evaluation) { e.RemainingGaps = []string{"Still missing"} },
		"missing evidence":  func(e *Evaluation) { e.EvidenceRefs = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			e := evaluation
			mutate(&e)
			b.Evaluation = &e
			if _, err := Parse(encode(t, b)); err == nil {
				t.Fatal("invalid assertion accepted")
			}
		})
	}
	b.Evaluation = nil
	first, err := Parse(encode(t, b))
	if err != nil {
		t.Fatal(err)
	}
	b.TaskIDs = []string{"TASK-2", "TASK-001"}
	second, err := Parse(encode(t, b))
	if err != nil {
		t.Fatal(err)
	}
	one, _ := first.Digest()
	two, _ := second.Digest()
	if one == two {
		t.Fatal("ordered task sequence lost in digest")
	}
}

func TestTaskIDPaddingHasBoundedSpelling(t *testing.T) {
	b := batchFixture(t)
	b.TaskIDs = []string{"TASK-" + strings.Repeat("0", (16<<10)-6) + "1"}
	if _, err := Parse(encode(t, b)); err != nil {
		t.Fatalf("exact padding limit: %v", err)
	}
	b.TaskIDs[0] = "TASK-" + strings.Repeat("0", (16<<10)-5) + "1"
	if _, err := Parse(encode(t, b)); err == nil {
		t.Fatal("oversize TASK spelling accepted")
	}
}
