package boardpolicy

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestParseRejectsEveryStructuralAmbiguity(t *testing.T) {
	for name, raw := range map[string]string{
		"empty": "", "utf8": string([]byte{0xff}), "root sequence": "[]",
		"duplicate root":     "schema-version: 1\nschema-version: 1\nboard-policy: {}",
		"unknown root":       "schema-version: 1\nboard-policy: {}\nextra: x",
		"string version":     "schema-version: '1'\nboard-policy: {}",
		"null block":         "schema-version: 1\nboard-policy: null",
		"duplicate block":    "schema-version: 1\nboard-policy: {zones: [], zones: []}",
		"duplicate status":   "schema-version: 1\nboard-policy: {zones: [manual], zone-status: {manual: done, manual: blocked}}",
		"duplicate row":      "schema-version: 1\nboard-policy: {transitions: [{from: todo, from: doing, to: [review]}]}",
		"null zones":         "schema-version: 1\nboard-policy: {zones: null}",
		"scalar zones":       "schema-version: 1\nboard-policy: {zones: manual}",
		"numeric zone":       "schema-version: 1\nboard-policy: {zones: [123]}",
		"complex key":        "schema-version: 1\nboard-policy: {? [zones] : []}",
		"missing targets":    "schema-version: 1\nboard-policy: {transitions: [{from: todo}]}",
		"empty targets":      "schema-version: 1\nboard-policy: {transitions: [{from: todo, to: []}]}",
		"missing source":     "schema-version: 1\nboard-policy: {transitions: [{to: [todo]}]}",
		"numeric target":     "schema-version: 1\nboard-policy: {transitions: [{from: todo, to: [1]}]}",
		"merge":              "schema-version: 1\nboard-policy: {<<: {zones: []}}",
		"custom tag":         "schema-version: 1\nboard-policy: !custom {}",
		"set tag":            "schema-version: 1\nboard-policy: !!set {}",
		"wrong mapping tag":  "schema-version: 1\nboard-policy: !!str {}",
		"wrong sequence tag": "schema-version: 1\nboard-policy: {zones: !!map []}",
		"nested tag":         "schema-version: 1\nboard-policy: {transitions: [!!set {from: todo, to: [doing]}]}",
		"multi empty doc":    "schema-version: 1\nboard-policy: {}\n---\n",
		"deep":               "schema-version: 1\nboard-policy: {zones: " + strings.Repeat("[", 20) + "x" + strings.Repeat("]", 20) + "}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw)); err == nil {
				t.Fatalf("accepted ambiguous document: %s", raw)
			}
		})
	}
}

func TestPolicySourceAndCanonicalBounds(t *testing.T) {
	base := "schema-version: 1\nboard-policy: {}\n#"
	atLimit := []byte(base + strings.Repeat(" ", maxPolicyBytes-len(base)))
	if _, err := Parse(atLimit); err != nil {
		t.Fatalf("exact source limit rejected: %v", err)
	}
	if _, err := Parse(append(atLimit, ' ')); err == nil {
		t.Fatal("source over limit accepted")
	}
	var input strings.Builder
	input.WriteString("schema-version: 1\nboard-policy: {zones: [")
	for i := 0; i < 8500; i++ {
		if i > 0 {
			input.WriteByte(',')
		}
		fmt.Fprintf(&input, "z%d", i)
	}
	input.WriteString("]}")
	if input.Len() > maxPolicyBytes {
		t.Fatal("fixture exceeds input limit before canonicalization")
	}
	if _, err := Parse([]byte(input.String())); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("canonical expansion limit not enforced: %v", err)
	}
}

func TestCanonicalGraphOrderAndEmptyDefaults(t *testing.T) {
	inputs := []string{
		"schema-version: 1\nboard-policy: {transitions: [{from: manual, to: [review, todo]}, {from: todo, to: [doing]}], zones: [manual]}",
		"board-policy: {zones: [manual], transitions: [{to: [doing], from: todo}, {to: [todo, review], from: manual}]}\nschema-version: 1",
	}
	var want []byte
	for _, input := range inputs {
		p, err := Parse([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := p.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if want != nil && !bytes.Equal(want, raw) {
			t.Fatal("graph ordering changed canonical bytes")
		}
		want = raw
	}
	defaultBytes, _ := Default().Canonical()
	for _, input := range []string{"schema-version: 1\nboard-policy: {}", "schema-version: 1\nboard-policy: {transitions: []}", string(defaultBytes)} {
		p, err := Parse([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := p.Canonical()
		if err != nil || !bytes.Equal(defaultBytes, raw) {
			t.Fatal("default graph canonicalization differs")
		}
	}
}

func FuzzPolicyCanonicalRoundTrip(f *testing.F) {
	f.Add([]byte("schema-version: 1\nboard-policy: {}"))
	f.Add([]byte("schema-version: 1\nboard-policy: {zones: [manual]}"))
	f.Add([]byte("schema-version: 1\nboard-policy: !!str {}"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := Parse(raw)
		if err != nil {
			return
		}
		canonical, err := p.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		q, err := Parse(canonical)
		if err != nil {
			t.Fatal(err)
		}
		again, err := q.Canonical()
		if err != nil || !bytes.Equal(canonical, again) {
			t.Fatal("canonical round-trip changed semantics")
		}
	})
}
