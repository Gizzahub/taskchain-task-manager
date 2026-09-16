package boardpolicy

import (
	"bytes"
	"strings"
	"testing"
)

func TestCanonicalDefaultGoldenAndRoundTrip(t *testing.T) {
	p := Default()
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema-version":1,"board-policy":{"zones":[],"zone-status":{},"transitions":[{"from":"blocked","to":["doing","todo"]},{"from":"doing","to":["blocked","review","todo"]},{"from":"done","to":["todo"]},{"from":"review","to":["doing","done"]},{"from":"todo","to":["doing"]}]}}`
	if string(raw) != want {
		t.Fatalf("canonical=%s\nwant=%s", raw, want)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := parsed.Canonical()
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("roundtrip=%s err=%v", again, err)
	}
	digest, err := p.Digest()
	if err != nil || digest != "91e1c66a4a78195f6c90d1e0309c2388a27f68933efacf2e2acbf1cbf3a56946" || digest != strings.ToLower(digest) {
		t.Fatalf("digest=%q err=%v", digest, err)
	}
}

func TestCanonicalSortAndSemanticDigest(t *testing.T) {
	a, err := Parse([]byte("schema-version: 1\nboard-policy:\n  zones: [zeta, manual]\n  zone-status:\n    zeta: done\n    manual: blocked\n  transitions:\n    - from: manual\n      to: [todo]\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse([]byte("schema-version: 1\nboard-policy:\n  zones: [manual, zeta]\n  zone-status:\n    manual: blocked\n    zeta: done\n  transitions:\n    - to: [todo]\n      from: manual\n"))
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := a.Canonical()
	rb, _ := b.Canonical()
	if !bytes.Equal(ra, rb) {
		t.Fatalf("lexical forms differ: %s / %s", ra, rb)
	}
	da, _ := a.Digest()
	db, _ := b.Digest()
	if da != db {
		t.Fatal("lexical reorder changed digest")
	}
	c, err := Parse([]byte("schema-version: 1\nboard-policy:\n  zones: [manual, zeta]\n  zone-status:\n    manual: done\n    zeta: done\n  transitions:\n    - from: manual\n      to: [todo]\n"))
	if err != nil {
		t.Fatal(err)
	}
	dc, _ := c.Digest()
	if dc == da {
		t.Fatal("semantic change did not change digest")
	}
}

func TestParseRejectsStrictShapeAndBounds(t *testing.T) {
	bad := []string{
		"board-policy: {}\n", "schema-version: 1\nboard-policy: {}\n---\nschema-version: 1\nboard-policy: {}\n",
		"schema-version: 1\nboard-policy:\n  zones: [manual, manual]\n", "schema-version: 1\nboard-policy:\n  nope: []\n", "schema-version: 1\nboard-policy:\n  zones: [manual]\n  zone-status: {manual: null}\n",
		"schema-version: 1\nboard-policy:\n  transitions: [{from: todo, to: [manual]}]\n", "schema-version: 1\nboard-policy: &x {}\n", "schema-version: 1\nboard-policy: *x\n",
		"schema-version: 1\nboard-policy:\n  transitions: [{from: todo, to: [doing], extra: x}]\n", "schema-version: 1\nboard-policy:\n  transitions: [{from: todo, to: [doing]}, {from: todo, to: [done]}]\n",
	}
	for i, raw := range bad {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("case %d accepted: %s", i, raw)
		}
	}
	if _, err := Parse([]byte("schema-version: 1\nboard-policy: {}\n" + strings.Repeat("#", maxPolicyBytes))); err == nil {
		t.Fatal("oversized input accepted")
	}
	var zero Policy
	if _, err := zero.Canonical(); err == nil {
		t.Fatal("zero policy canonicalized")
	}
}
