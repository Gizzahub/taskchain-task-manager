package taskruntime

import (
	"strings"
	"testing"
)

const canonicalTestProfile = `schema-version: 6
repository-id: synthetic
worktree-roots:
  fixture: /tmp/worktrees/synthetic
integration-provider: gz-git
integration-network-policy: no-fetch
lifecycle-authority:
  version: 1
  writer: none
  generation: "0123456789abcdef0123456789abcdef"
`

func TestCanonicalAuthorityDecode(t *testing.T) {
	for _, generation := range []string{`'0123456789abcdef0123456789abcdef'`, `"11111111111111111111111111111111"`} {
		if _, err := decodeCanonicalAuthority([]byte(strings.Replace(canonicalTestProfile, `"0123456789abcdef0123456789abcdef"`, generation, 1))); err != nil {
			t.Fatal(err)
		}
	}
	admin := "execution-administrators:\n  - actor: fixture\n    host: fixture-host\n    kind: agent\n"
	if _, err := decodeCanonicalAuthority([]byte(canonicalTestProfile + admin)); err != nil {
		t.Fatal(err)
	}
	for _, writer := range []string{"none", "ce", "task-manager"} {
		raw := strings.Replace(canonicalTestProfile, "writer: none", "writer: "+writer, 1)
		got, err := decodeCanonicalAuthority([]byte(raw))
		if err != nil || got.Config.SchemaVersion != 6 || got.Authority.Writer != writer {
			t.Fatalf("%s: %+v %v", writer, got, err)
		}
	}
	bad := map[string]string{
		"tagged-generation":        strings.Replace(canonicalTestProfile, `"0123456789abcdef0123456789abcdef"`, "!!str 11111111111111111111111111111111", 1),
		"tagged-quoted-generation": strings.Replace(canonicalTestProfile, `"0123456789abcdef0123456789abcdef"`, `!!str "0123456789abcdef0123456789abcdef"`, 1),
		"plain-generation":         strings.Replace(canonicalTestProfile, `"0123456789abcdef0123456789abcdef"`, "abcdefabcdefabcdefabcdefabcdefab", 1),
		"literal-generation":       strings.Replace(canonicalTestProfile, `"0123456789abcdef0123456789abcdef"`, "|-\n    11111111111111111111111111111111", 1),
		"folded-generation":        strings.Replace(canonicalTestProfile, `"0123456789abcdef0123456789abcdef"`, ">-\n    11111111111111111111111111111111", 1),
		"tagged-schema":            strings.Replace(canonicalTestProfile, "schema-version: 6", "schema-version: !!int 6", 1),
		"tagged-version":           strings.Replace(canonicalTestProfile, "version: 1", "version: !!int 1", 1),
		"admin-null":               canonicalTestProfile + "execution-administrators: null\n",
		"admin-extra":              canonicalTestProfile + strings.Replace(admin, "    kind: agent", "    kind: agent\n    extra: true", 1),
		"admin-identity":           canonicalTestProfile + strings.Replace(admin, "actor: fixture", "actor: Fixture", 1),
		"legacy":                   strings.Replace(canonicalTestProfile, "schema-version: 6", "schema-version: 5", 1),
		"future":                   strings.Replace(canonicalTestProfile, "schema-version: 6", "schema-version: 7", 1),
		"quoted-schema":            strings.Replace(canonicalTestProfile, "schema-version: 6", "schema-version: '6'", 1),
		"duplicate":                canonicalTestProfile + "repository-id: synthetic\n",
		"unknown":                  canonicalTestProfile + "unexpected: true\n",
		"nested-unknown":           strings.Replace(canonicalTestProfile, "  writer: none", "  unexpected: true\n  writer: none", 1),
		"null":                     strings.Replace(canonicalTestProfile, "writer: none", "writer: null", 1),
		"writer":                   strings.Replace(canonicalTestProfile, "writer: none", "writer: other", 1),
		"generation":               strings.Replace(canonicalTestProfile, "0123456789abcdef0123456789abcdef", "0123456789ABCDEF0123456789ABCDEF", 1),
		"numeric-generation":       strings.Replace(canonicalTestProfile, `"0123456789abcdef0123456789abcdef"`, "11111111111111111111111111111111", 1),
		"version":                  strings.Replace(canonicalTestProfile, "version: 1", "version: 2", 1),
		"anchor":                   strings.Replace(canonicalTestProfile, "writer: none", "writer: &owner none", 1),
		"alias":                    canonicalTestProfile + "extra: *owner\n",
		"merge":                    canonicalTestProfile + "<<: {}\n",
		"non-string-key":           canonicalTestProfile + "42: value\n",
		"documents":                canonicalTestProfile + "---\n{}\n",
		"relative-root":            strings.Replace(canonicalTestProfile, "/tmp/worktrees/synthetic", "relative/synthetic", 1),
		"root-basename":            strings.Replace(canonicalTestProfile, "/tmp/worktrees/synthetic", "/tmp/worktrees/foreign", 1),
		"empty-roots":              strings.Replace(canonicalTestProfile, "\n  fixture: /tmp/worktrees/synthetic", " {}", 1),
		"provider":                 strings.Replace(canonicalTestProfile, "gz-git", "other", 1),
		"network":                  strings.Replace(canonicalTestProfile, "no-fetch", "fetch", 1),
		"missing":                  strings.Replace(canonicalTestProfile, "repository-id: synthetic\n", "", 1),
		"invalid-utf8":             canonicalTestProfile + string([]byte{255}),
		"oversized":                canonicalTestProfile + strings.Repeat(" ", canonicalConfigMaxBytes),
		"empty":                    "",
	}
	for name, raw := range bad {
		t.Run(name, func(t *testing.T) {
			got, err := decodeCanonicalAuthority([]byte(raw))
			if err == nil || got.Config.RepositoryID != "" || got.Authority.Writer != "" {
				t.Fatalf("accepted invalid profile: %+v %v", got, err)
			}
		})
	}
	padded := canonicalTestProfile + "#" + strings.Repeat("x", canonicalConfigMaxBytes-len(canonicalTestProfile)-2) + "\n"
	if len(padded) != canonicalConfigMaxBytes {
		t.Fatal("fixture bound")
	}
	if _, err := decodeCanonicalAuthority([]byte(padded)); err != nil {
		t.Fatal(err)
	}
}
