package boardpolicy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchema5GoldenRoundTripAndAccessors(t *testing.T) {
	dir := filepath.Join("testdata", "file-roles-v5")
	raw, err := os.ReadFile(filepath.Join(dir, "policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "expected-canonical.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical=%s want=%s", got, want)
	}
	if !p.FileRolesEnabled() || !p.HasFileRole("AGENTS.md") || p.HasFileRole("missing.md") {
		t.Fatal("role presence mismatch")
	}
	role, ok := p.FileRoleV5("README.md")
	if !ok || role.Format != "live-card-paths-v1" || role.ContentSHA256 != "" {
		t.Fatalf("projection=%+v present=%v", role, ok)
	}
	roles := p.FileRolesV5()
	if len(roles) != 3 || roles[0].Path != "AGENTS.md" {
		t.Fatalf("roles=%+v", roles)
	}
	roles[0].Path = "changed"
	if current, _ := p.FileRoleV5("AGENTS.md"); current.Path != "AGENTS.md" {
		t.Fatal("accessor leaked state")
	}
	again, err := Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	round, err := again.Canonical()
	if err != nil || !bytes.Equal(round, got) {
		t.Fatalf("roundtrip err=%v", err)
	}
}

func TestSchema1Through4GoldenBytesUnchanged(t *testing.T) {
	dir := filepath.Join("testdata", "file-roles-v5")
	for _, version := range []string{"1", "2", "3", "4"} {
		t.Run(version, func(t *testing.T) {
			raw := readV5(t, filepath.Join(dir, "legacy-schema-"+version+".yaml"))
			want, err := os.ReadFile(filepath.Join(dir, "legacy-schema-"+version+".canonical.json"))
			if err != nil {
				t.Fatal(err)
			}
			p, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Canonical()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("canonical mismatch: %s", got)
			}
		})
	}
}

func TestSchema5RejectsRoleShapeAndMetadata(t *testing.T) {
	base := "schema-version: 5\nboard-policy:\n  modules: []\n  file-roles:\n    enabled: true\n    projections: []\n    guidance: []\n    templates: []\n"
	for name, raw := range map[string]string{
		"missing roles":    "schema-version: 5\nboard-policy: {modules: []}\n",
		"v4 role field":    strings.Replace(base, "    projections: []", "    projections: [{path: README.md, owner: owner, revision: r, marker: x}]", 1),
		"unknown":          strings.Replace(base, "    templates: []", "    templates: []\n    extra: x", 1),
		"unknown pipe key": strings.Replace(base, "    projections: []", "    projections: [{path: README.md, owner: o, source-revision: r, format: live-card-paths-v1, 'owner|source-revision': x}]", 1),
		"bad digest":       strings.Replace(base, "    guidance: []", "    guidance: [{path: AGENTS.md, owner: o, source-revision: r, content-sha256: BAD}]", 1),
		"cross kind":       strings.Replace(base, "    templates: []", "    templates: [{path: README.md, owner: o, source-revision: r, content-sha256: "+strings.Repeat("a", 64)+"}]", 1),
		"bad owner":        strings.Replace(base, "    guidance: []", "    guidance: [{path: AGENTS.md, owner: ' o', source-revision: r, content-sha256: "+strings.Repeat("a", 64)+"}]", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw)); err == nil {
				t.Fatal("accepted invalid schema 5")
			}
		})
	}
}

func TestNewV5IsExplicit(t *testing.T) {
	if _, err := NewV5(Declaration{}, nil); err == nil {
		t.Fatal("accepted missing modules")
	}
	if _, err := NewV5(Declaration{Modules: []string{}, FileRolesDeclared: true}, nil); err == nil {
		t.Fatal("accepted schema 4 declaration")
	}
	if _, err := NewV5(Declaration{Modules: []string{}, FileRoles: []FileRole{{Path: "README.md"}}}, nil); err == nil {
		t.Fatal("accepted deprecated file roles")
	}
}
