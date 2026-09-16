package taskstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func historyJournalFixture(t *testing.T) (transitionJournal, boardpolicy.Policy) {
	t.Helper()
	old := boardpolicy.Default()
	current, err := boardpolicy.New(boardpolicy.Declaration{Transitions: []boardpolicy.Transition{{From: "todo", To: []string{"done"}}}})
	if err != nil {
		t.Fatal(err)
	}
	oldRaw, err := old.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	currentRaw, err := current.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	j := journalFixture(t)
	j.SchemaVersion, j.PolicyDigest = 4, bytesDigest(currentRaw)
	j.PolicyAuthority = &policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}
	j.PolicyHistory = map[string][]byte{bytesDigest(oldRaw): oldRaw, bytesDigest(currentRaw): currentRaw}
	rec := &j.Records[0]
	rec.Kind, rec.Status, rec.PolicyDigest = "completed", "completed", bytesDigest(oldRaw)
	rec.Original, rec.Patched = nil, nil
	return j, current
}

func TestTransitionHistorySelectsReceiptPolicy(t *testing.T) {
	j, current := historyJournalFixture(t)
	if err := validateTransitionRecordWithPolicy(j.Records[0], current); err == nil {
		t.Fatal("fixture does not distinguish historical and active semantics")
	}
	raw, err := encodeTransitionJournal(j, current)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeTransitionJournal(raw)
	if err != nil || !reflect.DeepEqual(j, decoded) {
		t.Fatalf("history roundtrip: %v", err)
	}
	if err := validateTransitionRecords(decoded, current); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"missing", "wrong-policy", "pending", "wrong-active", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			j, current := historyJournalFixture(t)
			switch scenario {
			case "missing":
				delete(j.PolicyHistory, j.Records[0].PolicyDigest)
			case "wrong-policy":
				j.Records[0].PolicyDigest = j.PolicyDigest
			case "pending":
				digest := j.Records[0].PolicyDigest
				j.Records[0] = journalFixture(t).Records[0]
				j.Records[0].PolicyDigest = digest
			case "wrong-active":
				current = boardpolicy.Default()
			case "legacy":
				j.SchemaVersion, j.PolicyHistory = 3, nil
			}
			if _, err := encodeTransitionJournal(j, current); err == nil {
				t.Fatal("invalid historical binding accepted")
			}
		})
	}
	j.Records[0].PolicyDigest = ""
	if _, err := encodeTransitionJournal(j, current); err != nil {
		t.Fatalf("legacy receipt lost default semantics: %v", err)
	}
}

func TestTransitionHistoryRejectsMalformedWire(t *testing.T) {
	j, current := historyJournalFixture(t)
	raw, err := encodeTransitionJournal(j, current)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]json.RawMessage){
		"missing":      func(m map[string]json.RawMessage) { delete(m, "policyHistory") },
		"null":         func(m map[string]json.RawMessage) { m["policyHistory"] = json.RawMessage("null") },
		"empty":        func(m map[string]json.RawMessage) { m["policyHistory"] = json.RawMessage("{}") },
		"array":        func(m map[string]json.RawMessage) { m["policyHistory"] = json.RawMessage("[]") },
		"legacy":       func(m map[string]json.RawMessage) { m["schemaVersion"] = json.RawMessage("3") },
		"no-authority": func(m map[string]json.RawMessage) { delete(m, "policyAuthority") },
		"null-entry": func(m map[string]json.RawMessage) {
			m["policyHistory"] = json.RawMessage(`{"` + j.PolicyDigest + `":null}`)
		},
		"array-entry": func(m map[string]json.RawMessage) {
			m["policyHistory"] = json.RawMessage(`{"` + j.PolicyDigest + `":[123,125]}`)
		},
		"bad-digest": func(m map[string]json.RawMessage) {
			m["policyHistory"] = json.RawMessage(`{"` + j.PolicyDigest + `":"e30="}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			mutate(m)
			bad, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeTransitionJournal(bad); err == nil {
				t.Fatal("invalid history accepted")
			}
		})
	}
}

func TestTransitionHistoryValidatesUnreferencedEntries(t *testing.T) {
	j, current := historyJournalFixture(t)
	bad := []byte(`{"schemaVersion":99}`)
	j.PolicyHistory[bytesDigest(bad)] = bad
	if _, err := encodeTransitionJournal(j, current); err == nil {
		t.Fatal("unreferenced invalid policy accepted")
	}
}
