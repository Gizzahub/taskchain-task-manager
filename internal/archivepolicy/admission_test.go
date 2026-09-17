package archivepolicy

import (
	"errors"
	"testing"
)

func TestNormalAdmission(t *testing.T) {
	rules := Rules{AcceptedReviews: []string{"pass", "conditional", "waived"}}
	base := Facts{ID: "TASK-1", Kind: "task", Status: "done", WorkflowDone: true, Review: "pass", Evidence: "synthetic check log"}
	for _, tc := range []struct {
		name              string
		change            func(*Facts)
		allowed, eligible bool
	}{
		{"done", func(*Facts) {}, true, true},
		{"normalized review", func(f *Facts) { f.Review = " PASS " }, true, true},
		{"conditional", func(f *Facts) { f.Review = "conditional" }, true, true},
		{"waived", func(f *Facts) { f.Review = "waived" }, true, true},
		{"waived without evidence", func(f *Facts) { f.Review = "waived"; f.Evidence = " " }, false, false},
		{"review missing", func(f *Facts) { f.Review = "" }, false, false},
		{"not done", func(f *Facts) { f.Status = "review" }, false, false},
		{"metadata done only", func(f *Facts) { f.WorkflowDone = false }, true, false},
		{"non TASK", func(f *Facts) { f.ID = "PLAN-1" }, true, false},
		{"backlog", func(f *Facts) { f.Kind = "backlog" }, true, false},
		{"plan empty", func(f *Facts) { f.Kind = "plan" }, false, false},
		{"plan complete", func(f *Facts) { f.Kind = "plan"; f.References = []string{"TASK-2"} }, true, false},
		{"plan incomplete", func(f *Facts) { f.Kind = "plan"; f.References = []string{"TASK-3"} }, false, false},
		{"issue resolved no promotion", func(f *Facts) { f.Kind = "issue"; f.Resolution = "not applicable" }, true, false},
		{"issue unresolved", func(f *Facts) { f.Kind = "issue" }, false, false},
		{"issue promoted unfinished", func(f *Facts) { f.Kind = "issue"; f.Resolution = "promoted"; f.References = []string{"TASK-3"} }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.change(&f)
			got, err := EvaluateNormal(rules, f, func(id string) (bool, error) { return id == "TASK-2", nil })
			if err != nil || got.Allowed != tc.allowed || got.CompletionEligible != tc.eligible || got.Provenance != "normal-archive" {
				t.Fatalf("decision %+v, error %v", got, err)
			}
		})
	}
}

func TestAdmissionRefusesInvalidInputsAndResolverFailure(t *testing.T) {
	rules := Rules{AcceptedReviews: []string{"pass"}}
	for _, refs := range [][]string{{"TASK-1"}, {"TASK-2", "TASK-02"}, {"not-an-id"}} {
		if _, err := EvaluateNormal(rules, Facts{ID: "TASK-1", Kind: "plan", References: refs}, func(string) (bool, error) { return true, nil }); err == nil {
			t.Fatalf("accepted invalid references %v", refs)
		}
	}
	f := Facts{ID: "PLAN-1", Kind: "plan", References: []string{"TASK-2"}}
	if _, err := EvaluateNormal(rules, f, nil); err == nil {
		t.Fatal("accepted absent resolver")
	}
	want := errors.New("unreadable completion evidence")
	if _, err := EvaluateNormal(rules, f, func(string) (bool, error) { return false, want }); !errors.Is(err, want) {
		t.Fatalf("lost resolver error: %v", err)
	}
	for _, r := range []Rules{{}, {AcceptedReviews: []string{"pass", "pass"}}, {AcceptedReviews: []string{" pass"}}} {
		if _, err := EvaluateNormal(r, f, nil); err == nil {
			t.Fatal("accepted invalid rules")
		}
	}
	for _, kind := range []string{"archive", "supersede", "force", "legacy-adoption", ""} {
		f.Kind = kind
		if _, err := EvaluateNormal(rules, f, nil); err == nil {
			t.Fatalf("accepted %q", kind)
		}
	}
}
