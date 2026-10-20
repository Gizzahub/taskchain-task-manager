package roleprotocol

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// Each test here pins one rule: the fixture is consistent everywhere except
// the property under test, so removing that rule makes the test fail.

func TestRevisionValidatesTarget(t *testing.T) {
	f := loadFixture(t)
	// A zero role's missing binding reads as an empty policy digest, which
	// differs from the source and once let the revision pass.
	probe := Role{Adoption: Ack{Source: f.ack.Source}}
	wantErr(t, "unvalidated target", ValidateRevision(f.ack.Source, strings.Repeat("9", 32), probe), "revision target:")
}

func TestRevisionSupportOnlyChange(t *testing.T) {
	f := loadFixture(t)
	r := f.role(t)
	authority := strings.Repeat("9", 32)
	bound, err := r.SourceTuple(authority)
	if err != nil {
		t.Fatal(err)
	}
	// Same policy and current binding; only the support differs (§14.8).
	raw := mutate(t, f.v.Support.Input, func(m map[string]any) { dropProfile(t, m, "section-9/none") })
	support, err := ParseSupport(raw)
	if err != nil {
		t.Fatal(err)
	}
	next := r
	next.Support = support
	next.SupportDigest, _ = support.Digest()
	next.Adoption.Source, next.Adoption.SupportDigest = bound, next.SupportDigest
	next.AdoptionDigest, _ = next.Adoption.Digest()
	if next.SupportDigest == r.SupportDigest || next.CurrentBindingDigest != r.CurrentBindingDigest {
		t.Fatal("fixture assumption: only the support changes")
	}
	if err := next.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRevision(bound, strings.Repeat("a", 32), next); err != nil {
		t.Fatalf("a support-only change is a revision: %v", err)
	}
}

func TestAckTimesAreWholeSeconds(t *testing.T) {
	f := loadFixture(t)
	a := f.ack
	encoded, err := time.Parse(ackTimeLayout, a.ExpiresAt.Format(ackTimeLayout))
	if err != nil {
		t.Fatal(err)
	}
	a.ExpiresAt = a.ExpiresAt.Add(900 * time.Millisecond)
	wantErr(t, "expiresAt+900ms", a.Validate(), "whole-second UTC")
	if _, err := a.Digest(); err == nil {
		t.Error("a sub-second ack must not have a digest")
	}
	// The digest spells only whole seconds, so the window must close at the
	// encoded expiry, not up to a second later.
	wantErr(t, "window at encoded expiry", a.CheckWindow(encoded), "whole-second UTC")
	sub := f.ack
	sub.IssuedAt = sub.IssuedAt.Add(time.Nanosecond)
	wantErr(t, "issuedAt+1ns", sub.Validate(), "whole-second UTC")
}

func TestAckRootRefusesControlCharacter(t *testing.T) {
	f := loadFixture(t)
	root := "/fixture/re\x01po"
	_, err := ParseAck(mutate(t, f.v.Ack.Input, func(m map[string]any) {
		m["root"], m["participants"] = root, []any{root}
	}))
	wantErr(t, "control in root", err, "ack.root must be a normalized absolute path")
}

func TestHistoryLineageConflict(t *testing.T) {
	f := loadFixture(t)
	roles, err := os.ReadFile("../boardpolicy/testdata/file-roles-v5/expected-canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := `"content-sha256":"bdd46033e46f010d83272a618cc8e69d4d5731b2d542dfbf3559cca58562bb36"`
	if !bytes.Contains(roles, []byte(digest)) {
		t.Fatal("fixture assumption: guidance digest")
	}
	variant := func(from, to string) []byte {
		raw := bytes.Replace(roles, []byte(from), []byte(to), 1)
		p, err := boardpolicy.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if canonical, err := p.Canonical(); err != nil || !bytes.Equal(canonical, raw) {
			t.Fatal("variant policy is not canonical")
		}
		return raw
	}
	newRevision := variant(`"task-guide","source-revision":"v7"`, `"task-guide","source-revision":"v8"`)
	conflicting := variant(digest, `"content-sha256":"`+strings.Repeat("0", 64)+`"`)

	ok := f.boundHistory()
	addPolicy(t, ok, roles)
	addPolicy(t, ok, newRevision)
	if err := ok.Validate(HistoryBound); err != nil {
		t.Fatalf("a new source revision is not a conflict: %v", err)
	}
	bad := f.boundHistory()
	addPolicy(t, bad, roles)
	addPolicy(t, bad, conflicting)
	wantErr(t, "same owner and revision, other content", bad.Validate(HistoryBound), "file-role lineage conflict")
}

// Each legacy-mode rule is pinned by its own message; either alone would
// still refuse the other's input with a different reason.
func TestHistoryLegacyModeRules(t *testing.T) {
	f := loadFixture(t)
	withSelections := f.boundHistory()
	if err := withSelections.Validate(HistoryLegacySource); err == nil ||
		err.Error() != "legacy source history cannot hold selections" {
		t.Errorf("legacy mode with selections: %v", err)
	}
	selectedOnly := f.boundHistory()
	selectedOnly.Selections = map[string][]byte{}
	if err := selectedOnly.Validate(HistoryLegacySource); err == nil ||
		err.Error() != "legacy source history can only hold unselected bindings" {
		t.Errorf("legacy mode with a selected binding: %v", err)
	}
}

// rewriteRunReports applies edit to the report and every route evidence
// document, then rebinds the review to the new report digest.
func rewriteRunReports(t *testing.T, m map[string]any, edit func(doc map[string]any)) {
	t.Helper()
	acceptance := sub(m, "acceptance")
	digests := []string{acceptance["reportSHA256"].(string)}
	for _, r := range m["routes"].([]any) {
		digests = append(digests, r.(map[string]any)["evidenceSHA256"].(string))
	}
	for _, d := range digests {
		rewriteEvidence(t, m, d, edit)
	}
	rewriteEvidence(t, m, acceptance["reviewSHA256"].(string), func(doc map[string]any) {
		doc["reportSHA256"] = acceptance["reportSHA256"]
	})
}

func dropProfile(t *testing.T, m map[string]any, profile string) {
	t.Helper()
	var kept []any
	for _, p := range m["profiles"].([]any) {
		if p != profile {
			kept = append(kept, p)
		}
	}
	m["profiles"] = kept
	rewriteRunReports(t, m, func(doc map[string]any) {
		var rows []any
		for _, row := range doc["entries"].([]any) {
			if row.(map[string]any)["profile"] != profile {
				rows = append(rows, row)
			}
		}
		doc["entries"] = rows
	})
}

func setDecision(t *testing.T, m map[string]any, entry, decision string) {
	t.Helper()
	for _, r := range m["routes"].([]any) {
		if route := r.(map[string]any); route["entry"] == entry {
			route["decision"] = decision
		}
	}
	rewriteRunReports(t, m, func(doc map[string]any) {
		for _, row := range doc["entries"].([]any) {
			if row := row.(map[string]any); row["entry"] == entry {
				row["decision"] = decision
			}
		}
	})
}

func TestSupportV1RefusedEntry(t *testing.T) {
	f := loadFixture(t)
	raw := f.v.Support.Input
	// Control: the same consistent edit on an entry outside the v1 refuse set
	// is accepted, so only the refuse rule can reject the next case.
	if _, err := ParseSupport(mutate(t, raw, func(m map[string]any) { setDecision(t, m, "runtime.abort", "allow") })); err != nil {
		t.Fatalf("consistent allow of runtime.abort refused: %v", err)
	}
	_, err := ParseSupport(mutate(t, raw, func(m map[string]any) { setDecision(t, m, "store.bundle", "allow") }))
	if err == nil || err.Error() != "route store.bundle must be refuse in v1" {
		t.Fatalf("store.bundle allow: %v", err)
	}
}

func TestSupportRefusesEmptyRouteCommand(t *testing.T) {
	f := loadFixture(t)
	route := f.support.Routes[0].EvidenceSHA256
	_, err := ParseSupport(mutate(t, f.v.Support.Input, func(m map[string]any) {
		rewriteEvidence(t, m, route, func(doc map[string]any) { doc["command"] = "" })
	}))
	wantErr(t, "empty route command", err, ".command must be 1..1024 bytes")
}

func TestTransportSupportMustAcceptSelection(t *testing.T) {
	f := loadFixture(t)
	target := f.selections[2].Profile()
	support := decodeMap(t, f.v.Support.Input)
	dropProfile(t, support, target)
	raw, err := json.Marshal(support)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSupport(raw)
	if err != nil {
		t.Fatalf("consistent support without %s refused: %v", target, err)
	}
	digest, _ := parsed.Digest()
	good := f.transportJSON(t, f.selRaw[2], f.v.Ack.Input)
	_, err = ParseTransport(mutate(t, good, func(m map[string]any) {
		m["support"] = support
		sub(m, "ack")["supportDigest"] = digest
	}))
	if err == nil || err.Error() != "support does not accept profile "+target {
		t.Fatalf("support without the target profile: %v", err)
	}
}
