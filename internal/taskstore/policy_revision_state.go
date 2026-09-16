package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// The previous authority is a compare-and-swap generation. Every revision
// creates a fresh authority, including a later return to an older policy.
type policyRevision struct {
	PreviousAuthorityID string `json:"previousAuthorityId"`
	PreviousCanonical   []byte `json:"previousCanonical"`
	PreviousDigest      string `json:"previousDigest"`
}

func validatePolicyRevision(rev policyRevision, authority, digest string) error {
	if !sharedHex32.MatchString(rev.PreviousAuthorityID) || rev.PreviousAuthorityID == authority || !sharedHex64.MatchString(rev.PreviousDigest) || rev.PreviousDigest == digest {
		return errors.New("invalid policy revision generations or digests")
	}
	if len(rev.PreviousCanonical) == 0 || len(rev.PreviousCanonical) > 64<<10 || bytesDigest(rev.PreviousCanonical) != rev.PreviousDigest {
		return errors.New("invalid previous policy canonical binding")
	}
	p, err := boardpolicy.Parse(rev.PreviousCanonical)
	if err != nil {
		return err
	}
	canonical, err := p.Canonical()
	if err != nil || !bytes.Equal(canonical, rev.PreviousCanonical) {
		return errors.New("previous policy bytes are not canonical")
	}
	return nil
}

func validateActivationRevision(state policyActivationState) error {
	if state.SchemaVersion == 1 {
		if state.Revision != nil {
			return errors.New("initial activation cannot contain revision")
		}
		return nil
	}
	if state.Revision == nil {
		return errors.New("revision activation requires previous binding")
	}
	if err := validatePolicyRevision(*state.Revision, state.AuthorityID, state.Digest); err != nil {
		return err
	}
	if state.Plan.OriginalPolicy != state.Revision.PreviousDigest || state.Plan.OriginalActivation == "" || state.Plan.OriginalJournal == "" || len(state.Plan.IDTarget) != 0 || state.Plan.OriginalIDs != state.Plan.TargetIDs {
		return errors.New("revision must preserve IDs and bind exact previous activation and journal")
	}
	return nil
}

func validateRevisionEnvelope(raw []byte, shared bool) error {
	if err := rejectJSONSurrogates(raw); err != nil {
		return err
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	keys := []string{"schemaVersion", "phase", "authorityId", "scope", "namespace", "canonical", "digest", "plan"}
	if shared {
		keys = []string{"authorityId", "phase", "canonical", "digest", "pending"}
	}
	if revision, exists := fields["revision"]; exists {
		if !shared && string(fields["schemaVersion"]) != "2" {
			return errors.New("initial activation cannot contain revision")
		}
		if err := validateObjectShape(revision, []string{"previousAuthorityId", "previousCanonical", "previousDigest"}, nil); err != nil {
			return err
		}
		delete(fields, "revision")
		var err error
		raw, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	} else if !shared && string(fields["schemaVersion"]) == "2" {
		return errors.New("revision activation requires revision field")
	}
	return validateObjectShape(raw, keys, policyPlanKeys)
}
