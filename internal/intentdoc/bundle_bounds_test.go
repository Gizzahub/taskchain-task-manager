package intentdoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBundleExactBounds(t *testing.T) {
	base, err := ParseBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"title", "id", "config", "tasks"} {
		for _, over := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-over-%v", field, over), func(t *testing.T) {
				req, _ := base.Snapshot()
				extra := 0
				if over {
					extra = 1
				}
				switch field {
				case "title":
					req.Tasks[0].Title = strings.Repeat("x", 256+extra)
				case "id":
					req.Tasks[0].ID = "TASK-" + strings.Repeat("0", (16<<10)-6+extra) + "1"
				case "config":
					req.Tasks[0].Template.ValidationConfig = strings.Repeat("x", (64<<10)+extra)
				case "tasks":
					req.Tasks = nil
					for i := 0; i < 128+extra; i++ {
						req.Tasks = append(req.Tasks, TaskDraft{Key: fmt.Sprintf("task-%d", i), Title: "Task", DependsOn: []TaskReference{}})
					}
				}
				raw, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				_, err = ParseBundle(raw)
				if (err != nil) != over {
					t.Fatalf("boundary accepted=%v error=%v", !over, err)
				}
			})
		}
	}
}

func TestBundleCanonicalExpansionBound(t *testing.T) {
	base, _ := ParseBundle([]byte(validBundle))
	req, _ := base.Snapshot()
	for i := 0; i < 16; i++ {
		req.Batch.Constraints = append(req.Batch.Constraints, strings.Repeat("<", 3000))
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte(`\u003c`), []byte("<"))
	if len(raw) > MaxDocumentBytes {
		t.Fatal("fixture input too large")
	}
	if _, err := ParseBundle(raw); err == nil || !strings.Contains(err.Error(), "canonical bundle exceeds") {
		t.Fatalf("canonical bound: %v", err)
	}
}

func TestBundleNestedOwnershipAndValidJSONNull(t *testing.T) {
	raw := []byte(validBundle)
	doc, err := ParseBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := doc.Canonical()
	raw[0] = '!'
	snapshot, _ := doc.Snapshot()
	snapshot.Tasks[0].Template.Criteria[0] = "changed"
	snapshot.Tasks[1].DependsOn[0].Key = "changed"
	again, _ := doc.Canonical()
	if !bytes.Equal(want, again) {
		t.Fatal("nested mutation escaped")
	}
	var shape map[string]any
	if err := json.Unmarshal(want, &shape); err != nil {
		t.Fatal(err)
	}
	shape["tasks"] = nil
	nullRaw, err := json.Marshal(shape)
	if err != nil || !json.Valid(nullRaw) {
		t.Fatal("invalid null fixture")
	}
	if _, err := ParseBundle(nullRaw); err == nil || !strings.Contains(err.Error(), "null") {
		t.Fatalf("null accepted: %v", err)
	}
}
