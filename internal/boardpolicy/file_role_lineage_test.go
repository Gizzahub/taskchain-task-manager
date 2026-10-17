package boardpolicy

import (
	"bytes"
	"strings"
	"testing"
)

func lineagePolicy(t *testing.T, roles ...FileRoleV5) Policy {
	t.Helper()
	p, err := NewV5(Declaration{Modules: []string{}}, roles)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func lineageGuidance(path, owner, revision, digest string) FileRoleV5 {
	return FileRoleV5{Path: path, Kind: FileRoleGuidance, Owner: owner, SourceRevision: revision, ContentSHA256: strings.Repeat(digest, 64)}
}

func TestFileRoleLineageBindings(t *testing.T) {
	t.Parallel()
	a := lineageGuidance("AGENTS.md", "guide", "r1", "a")
	b := a
	b.Path, b.ContentSHA256 = "CLAUDE.md", strings.Repeat("b", 64)
	template := b
	template.Path, template.Kind = "TEMPLATE.md", FileRoleTemplate
	newRevision, newOwner, sameOtherPath := b, b, b
	newRevision.SourceRevision = "r2"
	newOwner.Owner = "other"
	sameOtherPath.ContentSHA256 = a.ContentSHA256
	for _, test := range []struct {
		name     string
		policies []Policy
		conflict bool
	}{
		{"empty", nil, false},
		{"same binding repeated", []Policy{lineagePolicy(t, a), lineagePolicy(t, a)}, false},
		{"different digest", []Policy{lineagePolicy(t, a), lineagePolicy(t, b)}, true},
		{"within one target", []Policy{lineagePolicy(t, a, b)}, true},
		{"same binding another path", []Policy{lineagePolicy(t, a, sameOtherPath)}, false},
		{"different kind", []Policy{lineagePolicy(t, a, template)}, false},
		{"new revision", []Policy{lineagePolicy(t, a), lineagePolicy(t, newRevision)}, false},
		{"new owner", []Policy{lineagePolicy(t, a), lineagePolicy(t, newOwner)}, false},
		{"role absent then returns", []Policy{lineagePolicy(t, a), lineagePolicy(t), lineagePolicy(t, b)}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := make([][]byte, len(test.policies))
			for i, p := range test.policies {
				var err error
				before[i], err = p.Canonical()
				if err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateFileRoleLineage(test.policies...)
			if (err != nil) != test.conflict {
				t.Fatalf("conflict=%v, err=%v", test.conflict, err)
			}
			for i, p := range test.policies {
				after, err := p.Canonical()
				if err != nil || !bytes.Equal(before[i], after) {
					t.Fatal("validator mutated a policy")
				}
			}
		})
	}
	// Separate authorities are separate calls; no global binding leaks between them.
	if err := ValidateFileRoleLineage(lineagePolicy(t, b)); err != nil {
		t.Fatal(err)
	}
}

func TestFileRoleLineageUsesExactTuples(t *testing.T) {
	t.Parallel()
	for _, roles := range [][]FileRoleV5{
		{lineageGuidance("AGENTS.md", "a|b", "c", "a"), lineageGuidance("CLAUDE.md", "a", "b|c", "b")},
		{lineageGuidance("AGENTS.md", "caf\u00e9", "r", "a"), lineageGuidance("CLAUDE.md", "cafe\u0301", "r", "b")},
		{lineageGuidance("AGENTS.md", "Owner", "r", "a"), lineageGuidance("CLAUDE.md", "owner", "r", "b")},
	} {
		if err := ValidateFileRoleLineage(lineagePolicy(t, roles...)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileRoleLineageDeterministicConflict(t *testing.T) {
	t.Parallel()
	a := lineagePolicy(t, lineageGuidance("AGENTS.md", "z", "r", "a"), lineageGuidance("CLAUDE.md", "a", "r", "a"))
	b := lineagePolicy(t, lineageGuidance("AGENTS.md", "z", "r", "b"), lineageGuidance("CLAUDE.md", "a", "r", "b"))
	want := `file-role lineage conflict for kind "guidance" owner "a" source revision "r"`
	for _, policies := range [][]Policy{{a, b}, {b, a}, {a, b, a}, {b, a, b}} {
		if err := ValidateFileRoleLineage(policies...); err == nil || err.Error() != want {
			t.Fatalf("conflict = %v, want %s", err, want)
		}
	}
}

func TestFileRoleLineageProjectionBindsFormat(t *testing.T) {
	t.Parallel()
	role := FileRoleV5{Path: "README.md", Kind: FileRoleProjection, Owner: "indexer", SourceRevision: "r1", Format: "live-card-paths-v1"}
	a := lineagePolicy(t, role)
	role.Path = "INDEX.md"
	b := lineagePolicy(t, role)
	if err := ValidateFileRoleLineage(a, b); err != nil {
		t.Fatal(err)
	}
	// Fault-inject a future format into an internal policy to exercise the binding
	// comparison independently. The public parser/constructor still rejects it.
	role.Format = "test-only-other-format"
	if _, err := NewV5(Declaration{Modules: []string{}}, []FileRoleV5{role}); err == nil {
		t.Fatal("synthetic format became public")
	}
	b.fileRolesV5[role.Path] = role
	if err := ValidateFileRoleLineage(a, b); err == nil {
		t.Fatal("format rebinding accepted")
	}
}

func TestFileRoleLineageLegacyDoesNotInventBindings(t *testing.T) {
	t.Parallel()
	p := lineagePolicy(t, lineageGuidance("AGENTS.md", "guide", "r", "a"))
	policies := []Policy{p}
	for _, version := range []string{"1", "2", "3", "4"} {
		legacy, err := Parse(readV5(t, "testdata/file-roles-v5/legacy-schema-"+version+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, legacy)
	}
	if err := ValidateFileRoleLineage(policies...); err != nil {
		t.Fatal(err)
	}
}
