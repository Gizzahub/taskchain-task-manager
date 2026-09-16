package boardpolicy

import (
	"reflect"
	"strings"
	"testing"
)

func TestDeclarationBoundaryRejections(t *testing.T) {
	for _, name := range []string{"", "Manual", " manual", "manual ", "a/b", "a\\b", ".ce", "..", "1manual", "a\x00b", "한글", "_archive", "issue", "backlog", "wip"} {
		if _, err := New(Declaration{Zones: []string{name}}); err == nil {
			t.Errorf("accepted zone %q", name)
		}
	}
	for _, rows := range [][]Transition{
		{{From: "todo", To: []string{"todo"}}},
		{{From: "todo", To: []string{"doing"}}, {From: "todo", To: []string{"review"}}},
		{{From: "", To: []string{"todo"}}},
		{{From: " todo", To: []string{"doing"}}},
		{{From: "todo", To: []string{"Doing"}}},
		{{From: "todo", To: []string{""}}},
		{{From: "todo", To: []string{"manual"}}},
	} {
		if _, err := New(Declaration{Zones: []string{"manual"}, Transitions: rows}); err == nil {
			t.Errorf("accepted graph %+v", rows)
		}
	}
}

func TestUnmappedAndTerminalParkingRemainNonWorkflow(t *testing.T) {
	for _, status := range []string{"", "done", "cancelled"} {
		d := Declaration{Zones: []string{"manual"}}
		if status != "" {
			d.ZoneStatus = map[string]string{"manual": status}
		}
		p, err := New(d)
		if err != nil || !p.Parked("manual") || p.Workflow("manual") || p.Allows("manual", "todo") {
			t.Fatalf("status=%q policy=%+v err=%v", status, p, err)
		}
		if got, ok := p.Status("manual"); got != status || ok != (status != "") {
			t.Fatalf("status=%q got=%q,%v", status, got, ok)
		}
	}
}

func TestPolicyOrderingAndDefaultEquivalence(t *testing.T) {
	for i := 0; i < 50; i++ {
		_, err := New(Declaration{ZoneStatus: map[string]string{"zeta": "blocked", "alpha": "done"}})
		if err == nil || !strings.Contains(err.Error(), `"alpha"`) {
			t.Fatalf("nondeterministic error: %v", err)
		}
	}
	p, err := New(Declaration{Zones: []string{"zeta", "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if dirs := p.KnownDirs(); !reflect.DeepEqual(dirs[len(dirs)-2:], []string{"alpha", "zeta"}) {
		t.Fatalf("dirs=%v", dirs)
	}
	empty, err := New(Declaration{})
	if err != nil || !reflect.DeepEqual(Default(), empty) {
		t.Fatalf("default differs from empty declaration: %v", err)
	}
}
