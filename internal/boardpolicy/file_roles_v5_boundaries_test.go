package boardpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchema5FixtureDigestsAndLegacyBaselines(t *testing.T) {
	dir := filepath.Join("testdata", "file-roles-v5")
	p, err := Parse(readV5(t, filepath.Join(dir, "policy.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d != strings.TrimSpace(string(readV5(t, filepath.Join(dir, "expected-digest.txt")))) {
		t.Fatal("v5 digest mismatch")
	}
	manifest := string(readV5(t, filepath.Join(dir, "legacy-canonical-sha256.txt")))
	for _, v := range []string{"1", "2", "3", "4"} {
		raw := readV5(t, filepath.Join(dir, "legacy-schema-"+v+".yaml"))
		want := readV5(t, filepath.Join(dir, "legacy-schema-"+v+".canonical.json"))
		p, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) || !strings.Contains(manifest, "schema-"+v+" "+digestV5Boundary(got)) {
			t.Fatalf("legacy %s changed", v)
		}
	}
}

func TestSchema5RoleBoundaries(t *testing.T) {
	base := "schema-version: 5\nboard-policy:\n  modules: []\n  file-roles:\n    enabled: true\n    projections: []\n    guidance: []\n    templates: []\n"
	for _, raw := range []string{strings.Replace(base, "enabled: true", "enabled: false", 1), strings.Replace(base, "    templates: []\n", "", 1), strings.Replace(base, "    guidance: []", "    guidance: [null]", 1), strings.Replace(base, "    guidance: []", "    guidance: [[x]]", 1), strings.Replace(base, "    projections: []", "    projections: [{path: README.md, owner: o, source-revision: r, format: other}]", 1), strings.Replace(base, "    templates: []", "    templates: []\n    guidance: []", 1)} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatal("accepted invalid role shape")
		}
	}
}

func TestNewV5MetadataLimitsAndDefensiveCopy(t *testing.T) {
	good := FileRoleV5{Path: "AGENTS.md", Kind: FileRoleGuidance, Owner: strings.Repeat("o", 128), SourceRevision: strings.Repeat("r", 128), ContentSHA256: strings.Repeat("a", 64)}
	input := []FileRoleV5{good}
	p, err := NewV5(Declaration{Modules: []string{}}, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Owner = "changed"
	got, _ := p.FileRoleV5("AGENTS.md")
	if got.Owner == "changed" {
		t.Fatal("caller mutation leaked")
	}
	for _, owner := range []string{strings.Repeat("o", 129), "o\x01", string([]byte{0xff}), strings.Repeat("é", 65)} {
		bad := good
		bad.Owner = owner
		if _, err := NewV5(Declaration{Modules: []string{}}, []FileRoleV5{bad}); err == nil {
			t.Fatal("accepted invalid owner")
		}
	}
	for _, revision := range []string{strings.Repeat("r", 129), "r\x01", string([]byte{0xff}), strings.Repeat("é", 65)} {
		bad := good
		bad.SourceRevision = revision
		if _, err := NewV5(Declaration{Modules: []string{}}, []FileRoleV5{bad}); err == nil {
			t.Fatal("accepted invalid revision")
		}
	}
	utf8Boundary := good
	utf8Boundary.Owner = strings.Repeat("é", 64)
	utf8Boundary.SourceRevision = strings.Repeat("é", 64)
	if _, err := NewV5(Declaration{Modules: []string{}}, []FileRoleV5{utf8Boundary}); err != nil {
		t.Fatal(err)
	}
}

func TestSchema5RolePathAndCanonicalOrder(t *testing.T) {
	base := "schema-version: 5\nboard-policy:\n  modules: []\n  file-roles:\n    enabled: true\n    projections: []\n    guidance: []\n    templates: []\n"
	for _, raw := range []string{strings.Replace(base, "    guidance: []", "    guidance: [{path: nested/AGENTS.md, owner: o, source-revision: r, content-sha256: "+strings.Repeat("a", 64)+"}]", 1), strings.Replace(base, "    templates: []", "    templates: [{path: AGENTS.md, owner: o, source-revision: r, content-sha256: "+strings.Repeat("a", 64)+"}]", 1), strings.Replace(base, "    guidance: []", "    guidance: [{path: AGENTS.md, owner: o, source-revision: r, content-sha256: "+strings.Repeat("a", 64)+"}, {path: AGENTS.md, owner: q, source-revision: s, content-sha256: "+strings.Repeat("b", 64)+"}]", 1)} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatal("accepted invalid path/duplicate/cross-kind")
		}
	}
	reordered := []byte("board-policy:\n  file-roles:\n    templates: []\n    guidance: [{content-sha256: '" + strings.Repeat("a", 64) + "', source-revision: r, owner: o, path: AGENTS.md}]\n    projections: [{format: live-card-paths-v1, source-revision: r, owner: o, path: README.md}]\n    enabled: true\n  modules: []\nschema-version: 5\n")
	p, err := Parse(reordered)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := NewV5(Declaration{Modules: []string{}}, []FileRoleV5{
		{Path: "README.md", Kind: FileRoleProjection, Owner: "o", SourceRevision: "r", Format: "live-card-paths-v1"},
		{Path: "AGENTS.md", Kind: FileRoleGuidance, Owner: "o", SourceRevision: "r", ContentSHA256: strings.Repeat("a", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := expected.Canonical()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("reordered policy changed canonical bytes: %s; %v", got, err)
	}
}

func readV5(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func digestV5Boundary(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
