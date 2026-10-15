package boardpolicy

import (
	"bytes"
	"strings"
	"testing"
)

func TestFileRolesSchema4RoundTripAndAccessors(t *testing.T) {
	raw := []byte(`schema-version: 4
board-policy:
  modules: []
  file-roles:
    enabled: true
    projections:
      - path: INDEX.md
        owner: task-indexer
        revision: v7
        marker: "generated: task-indexer v7"
      - path: README.md
        owner: task-indexer
        revision: v7
        marker: "generated: task-indexer v7"
    guidance: [CLAUDE.md, AGENTS.md]
    templates: [TEMPLATE.md]
`)
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !p.FileRolesEnabled() {
		t.Fatal("schema 4 did not enable file roles")
	}
	role, ok := p.FileRole("README.md")
	if !ok || role.Kind != FileRoleProjection || role.Owner != "task-indexer" || role.Revision != "v7" {
		t.Fatalf("README role=%+v present=%v", role, ok)
	}
	if _, ok := p.FileRole("notes.md"); ok {
		t.Fatal("undeclared file received a role")
	}
	roles := p.FileRoles()
	if len(roles) != 5 || roles[0].Path != "AGENTS.md" || roles[4].Path != "TEMPLATE.md" {
		t.Fatalf("sorted roles=%+v", roles)
	}
	roles[0].Path = "changed"
	if role, _ := p.FileRole("AGENTS.md"); role.Path != "AGENTS.md" {
		t.Fatal("roles accessor leaked policy state")
	}
	canonical, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema-version":4,"board-policy":{"zones":[],"zone-status":{},"transitions":[{"from":"blocked","to":["doing","todo"]},{"from":"doing","to":["blocked","review","todo"]},{"from":"done","to":["todo"]},{"from":"review","to":["doing","done"]},{"from":"todo","to":["doing"]}],"relocations":[],"kind-status":{},"modules":[],"file-roles":{"enabled":true,"projections":[{"path":"INDEX.md","owner":"task-indexer","revision":"v7","marker":"generated: task-indexer v7"},{"path":"README.md","owner":"task-indexer","revision":"v7","marker":"generated: task-indexer v7"}],"guidance":["AGENTS.md","CLAUDE.md"],"templates":["TEMPLATE.md"]}}}`
	if string(canonical) != want {
		t.Fatalf("canonical=%s want=%s", canonical, want)
	}
	parsed, err := Parse(canonical)
	if err != nil || !bytes.Equal(canonical, mustCanonical(t, parsed)) {
		t.Fatalf("round trip err=%v", err)
	}
}

func TestFileRolesFailClosed(t *testing.T) {
	base := `schema-version: 4
board-policy:
  modules: []
  file-roles:
    enabled: true
    projections: []
    guidance: []
    templates: []
`
	for name, raw := range map[string]string{
		"missing file roles":  "schema-version: 4\nboard-policy: {modules: []}\n",
		"missing modules":     "schema-version: 4\nboard-policy: {file-roles: {enabled: true, projections: [], guidance: [], templates: []}}\n",
		"version three":       strings.Replace(base, "schema-version: 4", "schema-version: 3", 1),
		"disabled":            strings.Replace(base, "enabled: true", "enabled: false", 1),
		"missing group":       strings.Replace(base, "    templates: []\n", "", 1),
		"unknown field":       strings.Replace(base, "    templates: []", "    templates: []\n    other: x", 1),
		"guidance nested":     strings.Replace(base, "    guidance: []", "    guidance: [tasks/AGENTS.md]", 1),
		"template wrong name": strings.Replace(base, "    templates: []", "    templates: [README.md]", 1),
		"hidden path":         strings.Replace(base, "    guidance: []", "    guidance: [.AGENTS.md]", 1),
		"glob path":           strings.Replace(base, "    guidance: []", "    guidance: [AGENTS*.md]", 1),
		"backslash path":      strings.Replace(base, "    guidance: []", "    guidance: ['AGENTS\\\\.md']", 1),
		"projection bad marker": `schema-version: 4
board-policy:
  modules: []
  file-roles:
    enabled: true
    projections: [{path: README.md, owner: owner, revision: rev, marker: unrelated}]
    guidance: []
    templates: []
`,
		"projection unknown field": `schema-version: 4
board-policy:
  modules: []
  file-roles:
    enabled: true
    projections: [{path: README.md, owner: owner, revision: rev, marker: owner rev, extra: x}]
    guidance: []
    templates: []
`,
		"owner surrounding whitespace":    projectionFixture(`owner: " owner"`, "revision: rev", "marker: owner rev"),
		"revision surrounding whitespace": projectionFixture("owner: owner", `revision: "rev "`, "marker: owner rev"),
		"owner newline":                   projectionFixture(`owner: "owner\nnext"`, "revision: rev", "marker: owner rev"),
		"marker newline":                  projectionFixture("owner: owner", "revision: rev", `marker: "owner rev\nnext"`),
		"marker control":                  projectionFixture("owner: owner", "revision: rev", `marker: "owner rev\u0001"`),
		"owner too long":                  projectionFixture("owner: "+strings.Repeat("o", 129), "revision: rev", "marker: "+strings.Repeat("o", 129)+" rev"),
		"marker too long":                 projectionFixture("owner: owner", "revision: rev", "marker: owner rev"+strings.Repeat("x", 504)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw)); err == nil {
				t.Fatalf("accepted invalid policy: %s", raw)
			}
		})
	}
	for _, raw := range []string{
		"schema-version: 1\nboard-policy: {}\n",
		"schema-version: 2\nboard-policy: {relocations: [], kind-status: {}}\n",
		"schema-version: 3\nboard-policy: {modules: []}\n",
	} {
		p, err := Parse([]byte(raw))
		if err != nil || p.FileRolesEnabled() || p.FileRoles() != nil {
			t.Fatalf("legacy policy changed: enabled=%v roles=%v err=%v", p.FileRolesEnabled(), p.FileRoles(), err)
		}
	}
}

func projectionFixture(owner, revision, marker string) string {
	return "schema-version: 4\nboard-policy:\n  modules: []\n  file-roles:\n    enabled: true\n    projections: [{path: README.md, " + owner + ", " + revision + ", " + marker + "}]\n    guidance: []\n    templates: []\n"
}
