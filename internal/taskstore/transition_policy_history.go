package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// History is part of the journal's atomic image, not a mutable policy lookup.
// Only completed receipts may select a historical policy; pending operations
// remain bound to the active policy even when their digest exists in history.
func transitionPolicyHistory(j transitionJournal) (map[string]boardpolicy.Policy, error) {
	if j.SchemaVersion != 4 {
		if len(j.PolicyHistory) != 0 {
			return nil, errors.New("legacy journal cannot contain policy history")
		}
		return nil, nil
	}
	if len(j.PolicyHistory) == 0 || j.PolicyHistory[j.PolicyDigest] == nil {
		return nil, errors.New("policy history must contain the active policy")
	}
	policies := make(map[string]boardpolicy.Policy, len(j.PolicyHistory))
	for digest, raw := range j.PolicyHistory {
		if !sharedHex64.MatchString(digest) || len(raw) == 0 || len(raw) > 64<<10 || bytesDigest(raw) != digest {
			return nil, errors.New("invalid historical policy digest or size")
		}
		p, err := boardpolicy.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("historical policy: %w", err)
		}
		canonical, err := p.Canonical()
		if err != nil || !bytes.Equal(canonical, raw) {
			return nil, errors.New("historical policy is not canonical")
		}
		policies[digest] = p
	}
	return policies, nil
}

func validatePolicyHistoryShape(raw json.RawMessage) error {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) == 0 {
		return errors.New("policy history must be a nonempty object")
	}
	for digest, value := range entries {
		// encoding/json accepts numeric arrays for []byte; the wire contract
		// deliberately permits only base64 strings, never null or arrays.
		var encoded string
		if !sharedHex64.MatchString(digest) || len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &encoded) != nil {
			return errors.New("policy history requires digest keys and base64 strings")
		}
	}
	return nil
}
