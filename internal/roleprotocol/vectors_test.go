package roleprotocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// testdata/vectors.json is the "vectors" member of the synthetic reference
// vector file published with the §14 role protocol (sha256 690b33fc…cd567,
// scope "Synthetic reference hash examples only"), copied unchanged except for
// dropping the calculator source. Its root, head, snapshot, operator and
// evidence are synthetic; the policy fixture is this repository's own boardpolicy testdata.
type vectorFile struct {
	PolicyFixture struct {
		Canonical string `json:"canonical"`
		Digest    string `json:"digest"`
	} `json:"policyFixture"`
	Selections []vectorCase `json:"selections"`
	Bindings   []vectorCase `json:"bindings"`
	Histories  struct {
		Legacy                 string `json:"legacy"`
		AllFourSelections      string `json:"allFourSelections"`
		InsertionOrderReversed string `json:"insertionOrderReversed"`
	} `json:"histories"`
	Support         vectorCase `json:"support"`
	Ack             vectorCase `json:"ack"`
	AbsentClaimsAck vectorCase `json:"absentClaimsAck"`
	ReferenceChecks struct {
		RejectedReferenceValues []string `json:"rejectedReferenceValues"`
	} `json:"referenceChecks"`
}

type vectorCase struct {
	Input     json.RawMessage `json:"input"`
	Canonical string          `json:"canonical"`
	Digest    string          `json:"digest"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// fixture bundles the decoded vectors every other test builds on.
type fixture struct {
	v          vectorFile
	policy     []byte
	policyHash string
	selections []Selector // s9/none, s9/s11, s10/none, s10/s11
	selRaw     [][]byte
	selDigest  []string
	bindings   []Binding // unselected, then selected per selection
	bindDigest []string
	support    Support
	ack        Ack
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{v: loadVectors(t)}
	f.policy = []byte(f.v.PolicyFixture.Canonical)
	f.policyHash = rawDigest(f.policy)
	for _, c := range f.v.Selections {
		s, err := ParseSelector(c.Input)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := s.Canonical()
		d, _ := s.Digest()
		f.selections, f.selRaw, f.selDigest = append(f.selections, s), append(f.selRaw, raw), append(f.selDigest, d)
	}
	for _, c := range f.v.Bindings {
		n, err := parseJSON(c.Input, maxBindingBytes, "binding")
		if err != nil {
			t.Fatal(err)
		}
		b, err := decodeBinding(n, "binding")
		if err != nil {
			t.Fatal(err)
		}
		d, _ := b.Digest()
		f.bindings, f.bindDigest = append(f.bindings, b), append(f.bindDigest, d)
	}
	var err error
	if f.support, err = ParseSupport(f.v.Support.Input); err != nil {
		t.Fatal(err)
	}
	if f.ack, err = ParseAck(f.v.Ack.Input); err != nil {
		t.Fatal(err)
	}
	return f
}

// boundHistory is the vector "allFourSelections" history.
func (f fixture) boundHistory() History {
	h := History{Policies: map[string][]byte{f.policyHash: f.policy}, Selections: map[string][]byte{}, Bindings: map[string]Binding{}}
	for i := range f.selections {
		h.Selections[f.selDigest[i]] = f.selRaw[i]
	}
	for i, b := range f.bindings {
		h.Bindings[f.bindDigest[i]] = b
	}
	return h
}

// historyJSON spells a history document with keys in the given order.
func historyJSON(h History, policyKeys, selectionKeys, bindingKeys []string) []byte {
	var b strings.Builder
	b.WriteString(`{"version":1,"policies":{`)
	for i, k := range policyKeys {
		fmt.Fprintf(&b, "%s%q:%q", comma(i), k, base64.StdEncoding.EncodeToString(h.Policies[k]))
	}
	b.WriteString(`},"selections":{`)
	for i, k := range selectionKeys {
		fmt.Fprintf(&b, "%s%q:%q", comma(i), k, base64.StdEncoding.EncodeToString(h.Selections[k]))
	}
	b.WriteString(`},"bindings":{`)
	for i, k := range bindingKeys {
		raw, _ := h.Bindings[k].Canonical()
		fmt.Fprintf(&b, "%s%q:%s", comma(i), k, raw)
	}
	b.WriteString(`}}`)
	return []byte(b.String())
}

func comma(i int) string {
	if i == 0 {
		return ""
	}
	return ","
}

func reversed(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

func TestVectorPolicyDigest(t *testing.T) {
	f := loadFixture(t)
	if f.policyHash != f.v.PolicyFixture.Digest {
		t.Fatalf("policy digest %s, want %s", f.policyHash, f.v.PolicyFixture.Digest)
	}
	repo, err := os.ReadFile("../boardpolicy/testdata/file-roles-v5/legacy-schema-1.canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repo, f.policy) {
		t.Fatal("vector policy fixture differs from the repository fixture")
	}
}

func TestVectorSelections(t *testing.T) {
	f := loadFixture(t)
	if len(f.v.Selections) != 4 {
		t.Fatalf("want 4 selection vectors, got %d", len(f.v.Selections))
	}
	for i, c := range f.v.Selections {
		if string(f.selRaw[i]) != c.Canonical {
			t.Errorf("selection %d canonical %s, want %s", i, f.selRaw[i], c.Canonical)
		}
		if f.selDigest[i] != c.Digest {
			t.Errorf("selection %d digest %s, want %s", i, f.selDigest[i], c.Digest)
		}
		if _, err := canonicalSelector([]byte(c.Canonical)); err != nil {
			t.Errorf("selection %d canonical bytes refused: %v", i, err)
		}
	}
}

func TestVectorBindings(t *testing.T) {
	f := loadFixture(t)
	if len(f.v.Bindings) != 5 || f.bindings[0].Kind != BindingUnselected {
		t.Fatal("want one unselected then four selected binding vectors")
	}
	for i, c := range f.v.Bindings {
		raw, _ := f.bindings[i].Canonical()
		if string(raw) != c.Canonical || f.bindDigest[i] != c.Digest {
			t.Errorf("binding %d: canonical %s digest %s, want %s %s", i, raw, f.bindDigest[i], c.Canonical, c.Digest)
		}
	}
	if d, _ := UnselectedBinding(f.policyHash).Digest(); d != f.v.Bindings[0].Digest {
		t.Errorf("UnselectedBinding digest %s", d)
	}
	for i := range f.selections {
		if d, _ := SelectedBinding(f.policyHash, f.selDigest[i]).Digest(); d != f.v.Bindings[i+1].Digest {
			t.Errorf("SelectedBinding %d digest %s, want %s", i, d, f.v.Bindings[i+1].Digest)
		}
	}
}

func TestVectorLegacyHistory(t *testing.T) {
	f := loadFixture(t)
	h, current, digest, err := LegacySourceHistory([][]byte{f.policy}, f.policyHash)
	if err != nil {
		t.Fatal(err)
	}
	if digest != f.v.Histories.Legacy {
		t.Fatalf("legacy history %s, want %s", digest, f.v.Histories.Legacy)
	}
	if current != f.v.Bindings[0].Digest || current != f.ack.Source.CurrentBindingDigest {
		t.Fatalf("legacy current binding %s", current)
	}
	if digest != f.ack.Source.HistoryDigest {
		t.Fatal("ack source history differs from computed legacy history")
	}
	keys := sortedKeys(h.Bindings)
	parsed, err := ParseHistory(historyJSON(h, sortedKeys(h.Policies), nil, keys), HistoryLegacySource)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := parsed.Digest(); d != f.v.Histories.Legacy {
		t.Fatalf("parsed legacy history %s", d)
	}
	if err := h.ValidateRecordedPolicies([]string{f.policyHash}); err != nil {
		t.Fatal(err)
	}
	if err := h.ValidateRecordedPolicies([]string{strings.Repeat("0", 64)}); err == nil {
		t.Fatal("a completed record outside history must be refused")
	}
}

func TestVectorBoundHistories(t *testing.T) {
	f := loadFixture(t)
	h := f.boundHistory()
	if err := h.Validate(HistoryBound); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.Digest(); d != f.v.Histories.AllFourSelections {
		t.Fatalf("allFourSelections %s, want %s", d, f.v.Histories.AllFourSelections)
	}
	pk, sk, bk := sortedKeys(h.Policies), sortedKeys(h.Selections), sortedKeys(h.Bindings)
	for name, raw := range map[string][]byte{
		"sorted":   historyJSON(h, pk, sk, bk),
		"reversed": historyJSON(h, reversed(pk), reversed(sk), reversed(bk)),
	} {
		parsed, err := ParseHistory(raw, HistoryBound)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d, _ := parsed.Digest(); d != f.v.Histories.InsertionOrderReversed {
			t.Fatalf("%s history %s, want %s", name, d, f.v.Histories.InsertionOrderReversed)
		}
	}
	if err := h.ValidateCurrent(f.ack.Target.BindingDigest, f.policyHash, true); err != nil {
		t.Fatal(err)
	}
	if err := h.ValidateCurrent(f.bindDigest[0], f.policyHash, true); err == nil {
		t.Fatal("an unselected target current binding must be refused")
	}
	legacy, _, _, _ := LegacySourceHistory([][]byte{f.policy}, f.policyHash)
	if err := ValidateHistoryExtension(legacy, h); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHistoryExtension(h, legacy); err == nil {
		t.Fatal("a target history that drops source entries must be refused")
	}
}

func TestVectorSupport(t *testing.T) {
	f := loadFixture(t)
	d, err := f.support.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d != f.v.Support.Digest || d != f.ack.SupportDigest {
		t.Fatalf("support digest %s, want %s", d, f.v.Support.Digest)
	}
	for _, s := range f.selections {
		if !f.support.Accepts(s) {
			t.Errorf("support must accept %s", s.Profile())
		}
	}
}

func TestVectorAcks(t *testing.T) {
	f := loadFixture(t)
	for name, c := range map[string]vectorCase{"ack": f.v.Ack, "absentClaimsAck": f.v.AbsentClaimsAck} {
		a, err := ParseAck(c.Input)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d, err := a.Digest()
		if err != nil || d != c.Digest {
			t.Fatalf("%s digest %s (%v), want %s", name, d, err, c.Digest)
		}
		if a.Target.HistoryDigest != f.v.Histories.AllFourSelections || a.Target.SelectionDigest != f.selDigest[2] ||
			a.Target.BindingDigest != f.bindDigest[3] {
			t.Fatalf("%s target does not match the computed section-10/none target", name)
		}
	}
	if f.ack.BoardRoot() != "/fixture/repo/tasks" {
		t.Fatalf("board root %s", f.ack.BoardRoot())
	}
}

func TestVectorReferenceChecks(t *testing.T) {
	f := loadFixture(t)
	t.Run("historyDeletionChangesDigest", func(t *testing.T) {
		h := f.boundHistory()
		before, _ := h.Digest()
		delete(h.Bindings, f.bindDigest[4])
		if after, _ := h.Digest(); after == before {
			t.Fatal("deleting a binding must change the history digest")
		}
	})
	t.Run("samePolicyDifferentSelectionsHaveDifferentBindings", func(t *testing.T) {
		seen := map[string]bool{}
		for _, d := range f.bindDigest {
			if seen[d] {
				t.Fatal("binding digests must be distinct")
			}
			seen[d] = true
		}
	})
	t.Run("ackExpiryChangesDigest", func(t *testing.T) {
		a := f.ack
		before, _ := a.Digest()
		a.ExpiresAt = a.ExpiresAt.Add(time.Second)
		if after, _ := a.Digest(); after == before {
			t.Fatal("expiry must change the ack digest")
		}
	})
	t.Run("claimsAbsentDistinctFromPresent", func(t *testing.T) {
		if f.v.Ack.Digest == f.v.AbsentClaimsAck.Digest {
			t.Fatal("absent and present claims must differ")
		}
	})
	t.Run("typeDomainsDistinct", func(t *testing.T) {
		seen := map[string]bool{}
		for _, d := range []string{domainSelection, domainBinding, domainHistory, domainSupport, domainCutover} {
			got := domainDigest(d, []byte("same"))
			if seen[got] {
				t.Fatal("domains must separate digests")
			}
			seen[got] = true
		}
	})
	t.Run("unicodeUTF8MapOrderStable", func(t *testing.T) {
		a, err := parseJSON([]byte(`{"é":1,"z":2,"a":3,"éx":4}`), 1024, "x")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := parseJSON([]byte(`{"éx":4,"a":3,"é":1,"z":2}`), 1024, "x")
		ea, _ := encodeE(nil, a)
		eb, _ := encodeE(nil, b)
		if !bytes.Equal(ea, eb) {
			t.Fatal("E must not depend on key order")
		}
		// UTF-8 byte order puts "z" (0x7a) before "é" (0xc3 0xa9).
		if bytes.Index(ea, []byte("z")) > bytes.Index(ea, []byte("é")) {
			t.Fatal("E keys must be sorted by UTF-8 bytes")
		}
	})
	t.Run("rejectedReferenceValues", func(t *testing.T) {
		if got := f.v.ReferenceChecks.RejectedReferenceValues; len(got) != 4 {
			t.Fatalf("unexpected rejected set %v", got)
		}
		if _, err := parseJSON([]byte("01"), 64, "n"); err == nil {
			t.Error("a leading-zero number must not parse")
		}
		for _, literal := range []string{"-1", "4294967296", "0.5", "1e0", "-0", "01"} {
			if _, err := encodeE(nil, &node{kind: jsonNumber, s: literal}); err == nil {
				t.Errorf("E must refuse %s", literal)
			}
		}
		// JSON has no byte type; any value outside the six kinds is refused.
		if _, err := encodeE(nil, &node{kind: jsonKind(99)}); err == nil {
			t.Error("E must refuse a bytes-like value")
		}
		n, _ := parseJSON([]byte("4294967295"), 64, "n")
		if e, err := encodeE(nil, n); err != nil || !bytes.Equal(e, append([]byte{'i', 0, 0, 0, 10}, "4294967295"...)) {
			t.Fatalf("max uint32 must encode as i+L, got %x %v", e, err)
		}
	})
}
