package boardpolicy

import (
	"bytes"
	"testing"
)

func TestV2RelocationAndKindStatusRoundTrip(t *testing.T) {
	p, err := New(Declaration{
		Zones:       []string{"manual"},
		Relocations: []Transition{{From: "manual", To: []string{"issue", "plan"}}, {From: "plan", To: []string{"backlog", "done"}}},
		KindStatus:  map[string]string{"issue": "review", "plan": "pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.AllowsRelocation("manual", "issue") || !p.AllowsRelocation("plan", "done") || p.AllowsRelocation("manual", "backlog") {
		t.Fatal("relocation graph mismatch")
	}
	if got, ok := p.KindStatus("issue"); !ok || got != "review" || !p.IsKind("plan") || p.IsKind("manual") {
		t.Fatalf("kind status/classification mismatch: %q %v", got, ok)
	}
	raw, err := p.Canonical()
	if err != nil || !bytes.Contains(raw, []byte(`"schema-version":2`)) || !bytes.Contains(raw, []byte(`"relocations"`)) || !bytes.Contains(raw, []byte(`"kind-status"`)) {
		t.Fatalf("canonical=%s err=%v", raw, err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := parsed.Canonical()
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("roundtrip=%s err=%v", again, err)
	}
}

func TestV2EmptyOptionalFieldsPreserveVersion(t *testing.T) {
	p, err := Parse([]byte("schema-version: 2\nboard-policy:\n  zones: []\n  zone-status: {}\n  transitions: []\n  relocations: []\n  kind-status: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Canonical()
	if err != nil || !bytes.Contains(raw, []byte(`"schema-version":2`)) || !bytes.Contains(raw, []byte(`"relocations":[]`)) || !bytes.Contains(raw, []byte(`"kind-status":{}`)) {
		t.Fatalf("canonical=%s err=%v", raw, err)
	}
}

func TestV2RelocationAndKindStatusRejectInvalidDeclarations(t *testing.T) {
	bad := []Declaration{
		{Relocations: []Transition{{From: "todo", To: []string{"plan", "doing"}}}},
		{Relocations: []Transition{{From: "todo", To: []string{"doing", "plan"}}}},
		{Zones: []string{"manual"}, Relocations: []Transition{{From: "manual", To: []string{"issue", "todo"}}}},
		{Relocations: []Transition{{From: "todo", To: []string{"doing"}}}},
		{Relocations: []Transition{{From: "todo", To: []string{"todo"}}}},
		{Relocations: []Transition{{From: "manual", To: []string{"manual"}}}, Zones: []string{"manual"}},
		{Relocations: []Transition{{From: "manual", To: []string{"archive"}}}, Zones: []string{"manual"}},
		{Relocations: []Transition{{From: "manual", To: []string{"issue", "issue"}}}, Zones: []string{"manual"}},
		{Relocations: []Transition{{From: "manual", To: []string{"issue"}}, {From: "manual", To: []string{"plan"}}}, Zones: []string{"manual"}},
		{Relocations: []Transition{{From: "missing", To: []string{"issue"}}}},
		{KindStatus: map[string]string{"manual": "done"}},
		{KindStatus: map[string]string{"issue": "unknown"}},
	}
	for i, declaration := range bad {
		if _, err := New(declaration); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	for _, raw := range []string{
		"schema-version: 1\nboard-policy:\n  zones: []\n  zone-status: {}\n  transitions: []\n  relocations: []\n",
		"schema-version: 1\nboard-policy:\n  zones: []\n  zone-status: {}\n  transitions: []\n  kind-status: {issue: review}\n",
		"schema-version: 2\nboard-policy:\n  zones: []\n  zone-status: {}\n  transitions: []\n  relocations: [{from: todo, to: [doing]}]\n",
	} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted invalid policy: %s", raw)
		}
	}
}
