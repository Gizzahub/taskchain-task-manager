package boardpolicy

import (
	"bytes"
	"testing"
)

func TestModulesSchema3RoundTripAndCopies(t *testing.T) {
	modules := []string{"zeta", "alpha-2"}
	p, err := New(Declaration{Modules: modules})
	if err != nil {
		t.Fatal(err)
	}
	modules[0] = "mutated"
	if p.IsModule("mutated") || !p.IsModule("zeta") {
		t.Fatal("declaration modules were not copied")
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema-version":3,"board-policy":{"zones":[],"zone-status":{},"transitions":[{"from":"blocked","to":["doing","todo"]},{"from":"doing","to":["blocked","review","todo"]},{"from":"done","to":["todo"]},{"from":"review","to":["doing","done"]},{"from":"todo","to":["doing"]}],"relocations":[],"kind-status":{},"modules":["alpha-2","zeta"]}}`
	if string(raw) != want {
		t.Fatalf("canonical=%s want=%s", raw, want)
	}
	parsed, err := Parse(raw)
	if err != nil || !bytes.Equal(raw, mustCanonical(t, parsed)) {
		t.Fatal(err)
	}
	got := parsed.Modules()
	got[0] = "mutated"
	if parsed.IsModule("mutated") || !parsed.IsModule("alpha-2") {
		t.Fatal("module output was not copied")
	}
}

func TestModulesSchemaVersionAndNames(t *testing.T) {
	valid := "schema-version: 3\nboard-policy:\n  modules: [alpha]\n"
	if _, err := Parse([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"schema-version: 3\nboard-policy: {}\n",
		"schema-version: 2\nboard-policy:\n  modules: [alpha]\n",
		"schema-version: 1\nboard-policy:\n  modules: []\n",
		"schema-version: 3\nboard-policy:\n  modules: [Todo]\n",
		"schema-version: 3\nboard-policy:\n  modules: [plan]\n",
		"schema-version: 3\nboard-policy:\n  zones: [alpha]\n  modules: [alpha]\n",
		"schema-version: 3\nboard-policy:\n  modules: [alpha, alpha]\n",
		"schema-version: 3\nboard-policy:\n  modules: null\n",
		"schema-version: 3\nboard-policy:\n  modules: [1]\n",
		"schema-version: 3\nboard-policy:\n  modules: [!!int 1]\n",
	} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted invalid modules policy: %s", raw)
		}
	}
	if got := Default().Modules(); got != nil {
		t.Fatalf("v1 modules=%v", got)
	}
}

func TestModulesEmptyRoundTripAndDigestOrder(t *testing.T) {
	p, err := New(Declaration{Modules: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Modules(); got == nil || len(got) != 0 {
		t.Fatalf("empty modules=%v", got)
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(raw)
	if err != nil || parsed.Modules() == nil || len(parsed.Modules()) != 0 {
		t.Fatalf("empty roundtrip modules=%v err=%v", parsed.Modules(), err)
	}
	a, err := New(Declaration{Modules: []string{"zeta", "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Declaration{Modules: []string{"alpha", "zeta"}})
	if err != nil {
		t.Fatal(err)
	}
	da, _ := a.Digest()
	db, _ := b.Digest()
	if da != db {
		t.Fatalf("module order changed digest: %s != %s", da, db)
	}
}

func TestModulesRejectExcludedAndZoneCollisions(t *testing.T) {
	for _, name := range []string{"evidence", "archive", "pending", "todo", "plan", "alpha"} {
		decl := Declaration{Modules: []string{name}}
		if name == "alpha" {
			decl.Zones = []string{name}
		}
		if _, err := New(decl); err == nil {
			t.Fatalf("module collision %q accepted", name)
		}
	}
	if _, err := New(Declaration{Modules: []string{".hidden"}}); err == nil {
		t.Fatal("dot-prefixed module accepted")
	}
}

func mustCanonical(t *testing.T, p Policy) []byte {
	t.Helper()
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
