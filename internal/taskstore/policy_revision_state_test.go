package taskstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestPolicyRevisionStateRejectsMalformedBindings(t *testing.T) {
	dir, options := localRevisionFixture(t)
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	policy, err := boardpolicy.Parse(revisionPolicyBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := preparePolicyRevision(r, policy, policyAuthorityBinding{AuthorityID: strings.Repeat("f", 32), Scope: "local"}, "", options)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := policyActivationBytes(state)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]json.RawMessage){
		"missing":        func(m map[string]json.RawMessage) { delete(m, "revision") },
		"null":           func(m map[string]json.RawMessage) { m["revision"] = json.RawMessage("null") },
		"initial-schema": func(m map[string]json.RawMessage) { m["schemaVersion"] = json.RawMessage("1") },
		"same-authority": func(m map[string]json.RawMessage) {
			rev := *state.Revision
			rev.PreviousAuthorityID = state.AuthorityID
			m["revision"], _ = json.Marshal(rev)
		},
		"bad-old-digest": func(m map[string]json.RawMessage) {
			rev := *state.Revision
			rev.PreviousDigest = strings.Repeat("f", 64)
			m["revision"], _ = json.Marshal(rev)
		},
		"noncanonical": func(m map[string]json.RawMessage) {
			rev := *state.Revision
			rev.PreviousCanonical = append([]byte(" "), rev.PreviousCanonical...)
			rev.PreviousDigest = bytesDigest(rev.PreviousCanonical)
			m["revision"], _ = json.Marshal(rev)
		},
		"unknown": func(m map[string]json.RawMessage) {
			var rev map[string]json.RawMessage
			_ = json.Unmarshal(m["revision"], &rev)
			rev["extra"] = json.RawMessage("true")
			m["revision"], _ = json.Marshal(rev)
		},
		"byte-array": func(m map[string]json.RawMessage) {
			var rev map[string]json.RawMessage
			_ = json.Unmarshal(m["revision"], &rev)
			rev["previousCanonical"] = json.RawMessage("[123,125]")
			m["revision"], _ = json.Marshal(rev)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			mutate(fields)
			bad, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.WriteFile(policyActivationFile, bad, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadPolicyActivation(r); err == nil {
				t.Fatal("malformed revision state accepted")
			}
		})
	}
}
