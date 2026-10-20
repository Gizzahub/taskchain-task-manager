package roleprotocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func (f fixture) transportJSON(t *testing.T, selection []byte, ack json.RawMessage) []byte {
	t.Helper()
	a := decodeMap(t, ack)
	source, _ := json.Marshal(a["source"])
	original, _ := json.Marshal(a["original"])
	return []byte(fmt.Sprintf(`{"version":1,"source":%s,"original":%s,"policy":%q,"selection":%q,"support":%s,"ack":%s}`,
		source, original, base64.StdEncoding.EncodeToString(f.policy), base64.StdEncoding.EncodeToString(selection),
		f.v.Support.Input, ack))
}

func TestTransport(t *testing.T) {
	f := loadFixture(t)
	good := f.transportJSON(t, f.selRaw[2], f.v.Ack.Input)
	tr, err := ParseTransport(good)
	if err != nil {
		t.Fatal(err)
	}
	if tr.PolicyDigest != f.policyHash || tr.Selector != f.selections[2] || !tr.Source.Equal(f.ack.Source) {
		t.Fatal("transport did not bind the prospective policy, selection and source")
	}
	if _, err := ParseTransport(f.transportJSON(t, f.selRaw[2], f.v.AbsentClaimsAck.Input)); err != nil {
		t.Fatalf("absent claims transport refused: %v", err)
	}

	_, err = ParseTransport(f.transportJSON(t, f.selRaw[0], f.v.Ack.Input))
	refuse(t, "selection differs from ack target", err)
	notCanonicalButValid := []byte(`{"amendment":"none","baseProfile":"section-10","contract":"task-manager:role-selection:v1"}`)
	if _, err := ParseTransport(f.transportJSON(t, notCanonicalButValid, f.v.Ack.Input)); err != nil {
		t.Fatalf("prospective selection input may use any key order: %v", err)
	}
	// The ack binds the canonical digest, so another spelling of the same
	// policy is the same target.
	respelled := mutate(t, good, func(m map[string]any) {
		m["policy"] = base64.StdEncoding.EncodeToString(append(append([]byte(nil), f.policy...), '\n'))
	})
	if _, err := ParseTransport(respelled); err != nil {
		t.Fatalf("respelled policy refused: %v", err)
	}
	otherPolicy, err := os.ReadFile("../boardpolicy/testdata/file-roles-v5/legacy-schema-2.canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(m map[string]any){
		"version 2":       func(m map[string]any) { m["version"] = json.Number("2") },
		"unknown field":   func(m map[string]any) { m["command"] = "x" },
		"missing support": func(m map[string]any) { delete(m, "support") },
		"source differs": func(m map[string]any) {
			sub(m, "source")["authorityId"] = strings.Repeat("7", 32)
		},
		"original differs": func(m map[string]any) {
			sub(m, "original")["journal"] = strings.Repeat("8", 64)
		},
		"claims differ": func(m map[string]any) {
			c := sub(m, "original", "claims")
			c["exists"], c["sha256"] = false, nil
		},
		"policy not base64": func(m map[string]any) { m["policy"] = "%%%" },
		"policy not a policy": func(m map[string]any) {
			m["policy"] = base64.StdEncoding.EncodeToString([]byte(`{}`))
		},
		"policy differs": func(m map[string]any) {
			m["policy"] = base64.StdEncoding.EncodeToString(otherPolicy)
		},
		"policy over 64KiB": func(m map[string]any) {
			m["policy"] = base64.StdEncoding.EncodeToString(make([]byte, maxProspectiveBytes+1))
		},
		"selection invalid": func(m map[string]any) {
			m["selection"] = base64.StdEncoding.EncodeToString([]byte(`{}`))
		},
		"support differs from ack": func(m map[string]any) {
			sub(m, "support", "acceptance")["command"] = "other"
		},
		"support drops target profile": func(m map[string]any) {
			sub(m, "support")["profiles"] = []any{"section-9/none"}
		},
	}
	for name, edit := range cases {
		_, err := ParseTransport(mutate(t, good, edit))
		refuse(t, name, err)
	}
	_, err = ParseTransport(append(good, make([]byte, maxTransportBytes)...))
	refuse(t, "over 1MiB", err)
}

// roleFixture builds the role an applied vector transport would persist.
func (f fixture) role(t *testing.T) Role {
	t.Helper()
	h := f.boundHistory()
	hd, _ := h.Digest()
	sd, _ := f.support.Digest()
	ad, _ := f.ack.Digest()
	return Role{History: h, HistoryDigest: hd, CurrentBindingDigest: f.ack.Target.BindingDigest,
		Support: f.support, SupportDigest: sd, Adoption: f.ack, AdoptionDigest: ad}
}

func (f fixture) roleJSON(t *testing.T, r Role, ack json.RawMessage) []byte {
	t.Helper()
	h := r.History
	history := historyJSON(h, sortedKeys(h.Policies), sortedKeys(h.Selections), sortedKeys(h.Bindings))
	return []byte(fmt.Sprintf(`{"protocol":1,"history":%s,"historyDigest":%q,"currentBindingDigest":%q,`+
		`"support":%s,"supportDigest":%q,"adoption":%s,"adoptionDigest":%q}`,
		history, r.HistoryDigest, r.CurrentBindingDigest, f.v.Support.Input, r.SupportDigest, ack, r.AdoptionDigest))
}

func TestRole(t *testing.T) {
	f := loadFixture(t)
	r := f.role(t)
	good := f.roleJSON(t, r, f.v.Ack.Input)
	parsed, err := ParseRole(good)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.AdoptionDigest != f.v.Ack.Digest || parsed.SupportDigest != f.v.Support.Digest ||
		parsed.HistoryDigest != f.v.Histories.AllFourSelections {
		t.Fatal("role digests differ from the vectors")
	}
	if err := parsed.CheckActivePolicy(f.policyHash); err != nil {
		t.Fatal(err)
	}
	refuse(t, "active policy differs", parsed.CheckActivePolicy(strings.Repeat("0", 64)))

	bogus := strings.Repeat("0", 64)
	cases := map[string]func(m map[string]any){
		"protocol 2":               func(m map[string]any) { m["protocol"] = json.Number("2") },
		"unknown field":            func(m map[string]any) { m["journal"] = bogus },
		"history digest differs":   func(m map[string]any) { m["historyDigest"] = bogus },
		"support digest differs":   func(m map[string]any) { m["supportDigest"] = bogus },
		"adoption digest differs":  func(m map[string]any) { m["adoptionDigest"] = bogus },
		"current binding absent":   func(m map[string]any) { m["currentBindingDigest"] = bogus },
		"current binding unselect": func(m map[string]any) { m["currentBindingDigest"] = f.bindDigest[0] },
		"current binding other selection": func(m map[string]any) {
			m["currentBindingDigest"] = f.bindDigest[1]
		},
		"support drops current profile": func(m map[string]any) {
			sub(m, "support")["profiles"] = []any{"section-9/none"}
		},
		"legacy history": func(m map[string]any) { sub(m, "history")["selections"] = map[string]any{} },
	}
	for name, edit := range cases {
		_, err := ParseRole(mutate(t, good, edit))
		refuse(t, name, err)
	}
	_, err = ParseRole(append(good, make([]byte, maxRoleBytes)...))
	refuse(t, "over 1MiB", err)

	// An adoption whose target names another history is refused even with
	// every stored digest recomputed.
	other := r
	other.Adoption.Target.HistoryDigest = f.v.Histories.Legacy
	other.AdoptionDigest, _ = other.Adoption.Digest()
	refuse(t, "adoption target history differs", other.Validate())
	other = r
	other.Adoption.SupportDigest = bogus
	other.AdoptionDigest, _ = other.Adoption.Digest()
	refuse(t, "adoption support differs", other.Validate())
}

func TestRoleBindingAndRevision(t *testing.T) {
	f := loadFixture(t)
	r := f.role(t)
	authority := strings.Repeat("9", 32)
	raw := fmt.Sprintf(`{"protocol":1,"authorityId":%q,"currentBindingDigest":%q,"historyDigest":%q,"supportDigest":%q,"adoptionDigest":%q}`,
		authority, r.CurrentBindingDigest, r.HistoryDigest, r.SupportDigest, r.AdoptionDigest)
	b, err := ParseRoleBinding([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Matches(authority, r); err != nil {
		t.Fatal(err)
	}
	refuse(t, "other authority", b.Matches(strings.Repeat("8", 32), r))
	for name, edit := range map[string]func(m map[string]any){
		"protocol 0":     func(m map[string]any) { m["protocol"] = json.Number("0") },
		"bad authority":  func(m map[string]any) { m["authorityId"] = "9" },
		"unknown field":  func(m map[string]any) { m["namespace"] = "" },
		"missing digest": func(m map[string]any) { delete(m, "adoptionDigest") },
		"null digest":    func(m map[string]any) { m["supportDigest"] = nil },
	} {
		_, err := ParseRoleBinding(mutate(t, []byte(raw), edit))
		refuse(t, name, err)
	}

	// Legacy source → first role-bound generation.
	if err := ValidateRevision(f.ack.Source, authority, r); err != nil {
		t.Fatal(err)
	}
	refuse(t, "same authority", ValidateRevision(f.ack.Source, f.ack.Source.AuthorityID, r))
	refuse(t, "bad authority", ValidateRevision(f.ack.Source, "x", r))
	bound, err := r.SourceTuple(authority)
	if err != nil {
		t.Fatal(err)
	}
	refuse(t, "adoption bound to another source", ValidateRevision(bound, strings.Repeat("a", 32), r))

	// A revision whose adoption names the bound source but changes nothing.
	noop := r
	noop.Adoption.Source = bound
	noop.AdoptionDigest, _ = noop.Adoption.Digest()
	if err := noop.Validate(); err != nil {
		t.Fatal(err)
	}
	refuse(t, "noop revision", ValidateRevision(bound, strings.Repeat("a", 32), noop))

	// Same policy, different selector: a new generation.
	selector := r
	selector.CurrentBindingDigest = f.bindDigest[4]
	selector.Adoption.Source = bound
	selector.Adoption.Target.SelectionDigest = f.selDigest[3]
	selector.Adoption.Target.BindingDigest = f.bindDigest[4]
	selector.AdoptionDigest, _ = selector.Adoption.Digest()
	if err := selector.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRevision(bound, strings.Repeat("a", 32), selector); err != nil {
		t.Fatalf("selector change is a revision: %v", err)
	}
}
