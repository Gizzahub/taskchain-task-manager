package roleprotocol

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// wantErr requires a refusal whose message contains want, so a test pins the
// rule it targets rather than any earlier or later refusal.
func wantErr(t *testing.T, name string, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("%s: got %v, want an error containing %q", name, err, want)
	}
}

// zonePolicy returns a canonical schema-1 policy of exactly size bytes whose
// single parked zone name starts with tag; distinct tags give distinct
// policies without any file-role lineage.
func (f fixture) zonePolicy(t *testing.T, tag string, size int) []byte {
	t.Helper()
	empty := []byte(`"zones":[]`)
	pad := size - len(f.policy) - len(`""`) - len(tag)
	if pad < 0 {
		t.Fatalf("zone policy of %d bytes is too small", size)
	}
	zone := `"zones":["` + tag + strings.Repeat("a", pad) + `"]`
	raw := bytes.Replace(f.policy, empty, []byte(zone), 1)
	p, err := boardpolicy.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if canonical, err := p.Canonical(); err != nil || !bytes.Equal(canonical, raw) || len(raw) != size {
		t.Fatalf("zone policy %s is not canonical at %d bytes", tag, size)
	}
	return raw
}

// addPolicy adds raw to h together with the unselected binding that keeps it
// referenced.
func addPolicy(t *testing.T, h History, raw []byte) {
	t.Helper()
	digest := rawDigest(raw)
	h.Policies[digest] = raw
	b := UnselectedBinding(digest)
	d, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	h.Bindings[d] = b
}

func TestHistoryPolicyCountLimit(t *testing.T) {
	f := loadFixture(t)
	h := f.boundHistory()
	for i := len(h.Policies); i < maxHistoryPolicies; i++ {
		addPolicy(t, h, f.zonePolicy(t, fmt.Sprintf("z%03d", i), len(f.policy)+16))
	}
	if err := h.Validate(HistoryBound); err != nil {
		t.Fatalf("%d policies refused: %v", maxHistoryPolicies, err)
	}
	addPolicy(t, h, f.zonePolicy(t, "zover", len(f.policy)+16))
	wantErr(t, "129 policies", h.Validate(HistoryBound), "history must hold 1..128 policies")
}

func TestHistoryPolicyTotalLimit(t *testing.T) {
	f := loadFixture(t)
	build := func(last int) History {
		h := f.boundHistory()
		for i := 0; i < 7; i++ {
			addPolicy(t, h, f.zonePolicy(t, fmt.Sprintf("z%d", i), maxHistoryPolicyBytes))
		}
		addPolicy(t, h, f.zonePolicy(t, "zlast", last))
		return h
	}
	last := maxHistoryPolicyTotal - len(f.policy) - 7*maxHistoryPolicyBytes
	if err := build(last).Validate(HistoryBound); err != nil {
		t.Fatalf("policies totalling exactly 512KiB refused: %v", err)
	}
	wantErr(t, "512KiB+1", build(last+1).Validate(HistoryBound), "history policies exceed 524288 bytes")
}

// cloneSupport copies every slice and map so a test can edit the fixture.
func cloneSupport(s Support) Support {
	s.Profiles, s.Routes, s.Evidence = slices.Clone(s.Profiles), slices.Clone(s.Routes), maps.Clone(s.Evidence)
	return s
}

// rebindEvidence replaces one evidence entry and every reference to it.
func rebindEvidence(s *Support, old string, raw []byte) {
	next := rawDigest(raw)
	delete(s.Evidence, old)
	s.Evidence[next] = raw
	if s.Acceptance.ReviewSHA256 == old {
		s.Acceptance.ReviewSHA256 = next
	}
	for i := range s.Routes {
		if s.Routes[i].EvidenceSHA256 == old {
			s.Routes[i].EvidenceSHA256 = next
		}
	}
}

func TestSupportEvidenceCountLimit(t *testing.T) {
	f := loadFixture(t)
	with := func(n int) Support {
		s := cloneSupport(f.support)
		for i := 0; len(s.Evidence) < n; i++ {
			raw := []byte(fmt.Sprintf("extra %d", i))
			s.Evidence[rawDigest(raw)] = raw
		}
		return s
	}
	// Every entry beyond the referenced ones is unreferenced, so at the limit
	// the count rule must stay silent and the unreferenced rule refuses.
	wantErr(t, "256 entries", with(maxEvidenceEntries).Validate(), "support.evidence holds an unreferenced entry")
	wantErr(t, "257 entries", with(maxEvidenceEntries+1).Validate(), "support.evidence holds more than 256 entries")
}

func TestSupportEvidenceTotalLimit(t *testing.T) {
	f := loadFixture(t)
	// Whitespace keeps each document valid. The report is left alone because
	// the review binds its digest.
	with := func(total int) Support {
		s := cloneSupport(f.support)
		remaining := total
		for _, raw := range s.Evidence {
			remaining -= len(raw)
		}
		targets := []string{s.Acceptance.ReviewSHA256}
		for _, r := range s.Routes {
			targets = append(targets, r.EvidenceSHA256)
		}
		for _, digest := range targets {
			raw := s.Evidence[digest]
			n := min(remaining, maxEvidenceEntryBytes-len(raw))
			if n <= 0 {
				continue
			}
			remaining -= n
			rebindEvidence(&s, digest, append(slices.Clone(raw), bytes.Repeat([]byte(" "), n)...))
		}
		if remaining != 0 {
			t.Fatalf("could not pad evidence to %d bytes", total)
		}
		return s
	}
	if err := with(maxEvidenceTotalBytes).Validate(); err != nil {
		t.Fatalf("evidence totalling exactly 128KiB refused: %v", err)
	}
	wantErr(t, "128KiB+1", with(maxEvidenceTotalBytes+1).Validate(), "support.evidence exceeds 131072 decoded bytes")
}

// padded spells doc with inner whitespace so its outer JSON is size bytes.
func padded(t *testing.T, doc []byte, size int) []byte {
	t.Helper()
	doc = bytes.TrimSpace(doc)
	if doc[0] != '{' || size < len(doc) {
		t.Fatalf("cannot pad %d bytes to %d", len(doc), size)
	}
	return append(append([]byte("{"), bytes.Repeat([]byte(" "), size-len(doc))...), doc[1:]...)
}

// The §14.1 byte limit applies to an embedded document's outer JSON, which
// the enclosing document's larger limit would otherwise admit.
func TestEmbeddedOuterJSONLimits(t *testing.T) {
	f := loadFixture(t)
	support, ack := []byte(f.v.Support.Input), []byte(f.v.Ack.Input)
	for _, c := range []struct {
		name    string
		limit   int
		message string
		build   func(size int) error
	}{
		{"support in transport", maxSupportBytes, "support exceeds 262144 bytes", func(size int) error {
			_, err := ParseTransport(f.transportRaw(t, f.selRaw[2], padded(t, support, size), ack))
			return err
		}},
		{"ack in transport", maxAckBytes, "ack exceeds 65536 bytes", func(size int) error {
			_, err := ParseTransport(f.transportRaw(t, f.selRaw[2], support, padded(t, ack, size)))
			return err
		}},
		{"embedded role", maxRoleBytes, "role exceeds 1048576 bytes", func(size int) error {
			n, err := parseJSON(padded(t, f.roleJSON(t, f.role(t), f.v.Ack.Input), size), 2*maxRoleBytes, "journal.role")
			if err != nil {
				t.Fatal(err)
			}
			_, err = decodeRole(n)
			return err
		}},
	} {
		if err := c.build(c.limit); err != nil {
			t.Errorf("%s at its limit refused: %v", c.name, err)
		}
		wantErr(t, c.name+" over its limit", c.build(c.limit+1), c.message)
	}
}

// A literal must not copy the remaining input; 1MiB of nulls once took
// seconds. The bound is loose on purpose: it only catches quadratic work.
func TestParseJSONLiteralsAreLinear(t *testing.T) {
	const count = 1 << 18
	raw := append(append([]byte("["), bytes.Repeat([]byte("null,"), count-1)...), "null]"...)
	start := time.Now()
	n, err := parseJSON(raw, len(raw), "nulls")
	if err != nil || len(n.items) != count {
		t.Fatalf("nulls: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("parsing %d bytes of literals took %s", len(raw), elapsed)
	}
}
