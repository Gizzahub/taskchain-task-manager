package archivepolicy

import (
	"bytes"
	"strings"
	"testing"
)

const configFixture = `schema-version: 1
archive-admission:
  fields:
    review: review-result
    evidence: review-proof
    resolution: disposition
    promoted-to: promoted
    children: child-ids
  accepted-reviews: [pass, conditional, waived]
`

func TestConfigCanonicalRoundTrip(t *testing.T) {
	c, err := ParseConfig([]byte(configFixture))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := got.Canonical()
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("unstable canonical config: %s %v", again, err)
	}
	a, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	c.Admission.AcceptedReviews = []string{"WAIVED", "PASS", "conditional"}
	b, err := c.Digest()
	if err != nil || a != b {
		t.Fatalf("equivalent vocabulary digest differs %s %s %v", a, b, err)
	}
	c.Admission.Fields.Evidence = "other-proof"
	d, _ := c.Digest()
	if d == a {
		t.Fatal("field binding absent from digest")
	}
}

func TestConfigStrictRefusals(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown root":       configFixture + "extra: yes\n",
		"duplicate root":     configFixture + "schema-version: 1\n",
		"missing schema":     strings.Replace(configFixture, "schema-version: 1\n", "", 1),
		"wrong version":      strings.Replace(configFixture, "version: 1", "version: 2", 1),
		"quoted version":     strings.Replace(configFixture, "version: 1", "version: '1'", 1),
		"unknown field":      strings.Replace(configFixture, "review: review-result", "review: review-result\n    other: x", 1),
		"missing field":      strings.Replace(configFixture, "    evidence: review-proof\n", "", 1),
		"null field":         strings.Replace(configFixture, "review: review-result", "review: null", 1),
		"number field":       strings.Replace(configFixture, "review: review-result", "review: 123", 1),
		"empty field":        strings.Replace(configFixture, "review: review-result", "review: ''", 1),
		"mapped alias":       strings.Replace(strings.Replace(configFixture, "review: review-result", "review: &name review-result", 1), "evidence: review-proof", "evidence: *name", 1),
		"merge":              strings.Replace(configFixture, "    review: review-result", "    <<: {review: review-result}", 1),
		"mapping collision":  strings.Replace(configFixture, "evidence: review-proof", "evidence: review-result", 1),
		"empty reviews":      strings.Replace(configFixture, "[pass, conditional, waived]", "[]", 1),
		"null review":        strings.Replace(configFixture, "[pass, conditional, waived]", "[pass, null]", 1),
		"review duplicate":   strings.Replace(configFixture, "[pass, conditional, waived]", "[pass, PASS]", 1),
		"review scalar":      strings.Replace(configFixture, "[pass, conditional, waived]", "pass", 1),
		"multiple documents": configFixture + "---\n{}\n",
		"oversized":          configFixture + strings.Repeat(" ", ConfigLimit),
		"invalid utf8":       configFixture + string([]byte{255}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(raw)); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestConfigCanonicalDoesNotMutateAndValidatesDirectValues(t *testing.T) {
	c, err := ParseConfig([]byte(configFixture))
	if err != nil {
		t.Fatal(err)
	}
	before := strings.Join(c.Admission.AcceptedReviews, ",")
	if _, err := c.Canonical(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Admission.AcceptedReviews, ",") != before {
		t.Fatal("canonical mutated input")
	}
	c.Admission.AcceptedReviews = []string{string([]byte{255})}
	if _, err := c.Canonical(); err == nil {
		t.Fatal("accepted invalid UTF8 review")
	}
}

func TestConfigRejectsTaggedCollectionsAndCanonicalOverflow(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(configFixture, "archive-admission:\n", "archive-admission: !custom\n", 1),
		strings.Replace(configFixture, "  fields:\n", "  fields: !!str\n", 1),
		strings.Replace(configFixture, "[pass, conditional, waived]", "!custom [pass, conditional, waived]", 1),
		strings.Replace(configFixture, "review: review-result", "review: '"+strings.Repeat("&", 12000)+"'", 1),
	} {
		if len(raw) > ConfigLimit {
			t.Fatal("fixture does not isolate canonical expansion")
		}
		if _, err := ParseConfig([]byte(raw)); err == nil {
			t.Fatal("accepted noncanonical config")
		}
	}
}
