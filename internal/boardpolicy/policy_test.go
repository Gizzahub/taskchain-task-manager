package boardpolicy

import (
	"reflect"
	"testing"
)

func TestDefaultPolicyHasWorkflowStatusesAndEdges(t *testing.T) {
	p := Default()
	if !reflect.DeepEqual(p.KnownDirs(), []string{"todo", "doing", "review", "blocked", "done", "issue", "plan", "backlog", "archive", "_archive"}) {
		t.Fatalf("dirs=%v", p.KnownDirs())
	}
	for zone, want := range map[string]string{"todo": "pending", "doing": "in-progress", "review": "review", "blocked": "blocked", "done": "done"} {
		if !p.Workflow(zone) || p.Parked(zone) {
			t.Errorf("zone=%s classification wrong", zone)
		}
		if got, ok := p.Status(zone); !ok || got != want {
			t.Errorf("status(%s)=%q,%v", zone, got, ok)
		}
	}
	allowed := map[string]map[string]bool{"todo": {"doing": true}, "doing": {"todo": true, "review": true, "blocked": true}, "review": {"doing": true, "done": true}, "blocked": {"todo": true, "doing": true}, "done": {"todo": true}}
	for _, from := range []string{"todo", "doing", "review", "blocked", "done"} {
		for _, to := range []string{"todo", "doing", "review", "blocked", "done"} {
			if p.Allows(from, to) != allowed[from][to] {
				t.Errorf("edge %s -> %s = %v, want %v", from, to, p.Allows(from, to), allowed[from][to])
			}
		}
	}
}

func TestCustomParkedZoneIsNotWorkflow(t *testing.T) {
	p, err := New(Declaration{Zones: []string{"manual"}, ZoneStatus: map[string]string{"manual": "blocked"}, Transitions: []Transition{{From: "todo", To: []string{"doing"}}, {From: "manual", To: []string{"todo"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Workflow("manual") || !p.Parked("manual") {
		t.Fatal("manual classification wrong")
	}
	if got, ok := p.Status("manual"); !ok || got != "blocked" {
		t.Fatalf("manual status=%q,%v", got, ok)
	}
	if !p.Allows("manual", "todo") || p.Allows("manual", "doing") || p.Allows("todo", "done") {
		t.Fatal("custom graph semantics wrong")
	}
	if !reflect.DeepEqual(p.KnownDirs(), []string{"todo", "doing", "review", "blocked", "done", "issue", "plan", "backlog", "archive", "_archive", "manual"}) {
		t.Fatalf("dirs=%v", p.KnownDirs())
	}
}

func TestInvalidDeclarationsAreDeterministicAndFailClosed(t *testing.T) {
	bad := []Declaration{
		{Zones: []string{"todo"}}, {Zones: []string{"in_progress"}}, {Zones: []string{"plan"}}, {Zones: []string{"archive"}}, {Zones: []string{"evidence"}},
		{Zones: []string{"manual", "manual"}}, {ZoneStatus: map[string]string{"manual": "blocked"}}, {Zones: []string{"manual"}, ZoneStatus: map[string]string{"manual": "unknown"}},
		{Transitions: []Transition{{From: "todo", To: []string{"manual"}}}}, {Transitions: []Transition{{From: "manual", To: []string{"todo"}}}},
		{Zones: []string{"manual"}, Transitions: []Transition{{From: "todo", To: nil}}}, {Zones: []string{"manual"}, Transitions: []Transition{{From: "todo", To: []string{"doing", "doing"}}}},
	}
	for i, d := range bad {
		if _, err := New(d); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	var zero Policy
	if zero.Workflow("todo") || zero.Parked("manual") || zero.Allows("todo", "doing") {
		t.Fatal("zero policy did not fail closed")
	}
	if _, ok := zero.Status("todo"); ok || len(zero.KnownDirs()) != 0 {
		t.Fatal("zero policy exposed state")
	}
}

func TestPolicyCopiesInputsAndOutputs(t *testing.T) {
	zones := []string{"manual"}
	statuses := map[string]string{"manual": "blocked"}
	targets := []string{"todo"}
	p, err := New(Declaration{Zones: zones, ZoneStatus: statuses, Transitions: []Transition{{From: "manual", To: targets}}})
	if err != nil {
		t.Fatal(err)
	}
	zones[0] = "todo"
	statuses["manual"] = "done"
	targets[0] = "done"
	if !p.Parked("manual") || p.StatusValueForTest("manual") != "blocked" || !p.Allows("manual", "todo") || p.Allows("manual", "done") {
		t.Fatal("input mutation leaked")
	}
	dirs := p.KnownDirs()
	dirs[0] = "mutated"
	if p.KnownDirs()[0] != "todo" {
		t.Fatal("output mutation leaked")
	}
}

// StatusValueForTest is deliberately only used inside this package to make the
// copy test readable without exposing mutable policy state.
func (p Policy) StatusValueForTest(zone string) string { value, _ := p.Status(zone); return value }
