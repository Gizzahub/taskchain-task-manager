package taskstore

import (
	"bytes"
	"errors"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func revisedPolicyJournal(j transitionJournal, policy boardpolicy.Policy, binding policyAuthorityBinding, rev policyRevision) (transitionJournal, error) {
	canonical, err := policy.Canonical()
	if err != nil {
		return j, err
	}
	digest := bytesDigest(canonical)
	if err := validatePolicyRevision(rev, binding.AuthorityID, digest); err != nil {
		return j, err
	}
	if j.PolicyAuthority == nil || j.PolicyAuthority.Scope != binding.Scope || j.PolicyAuthority.Namespace != binding.Namespace {
		return j, errors.New("revision journal authority scope mismatch")
	}
	old, err := boardpolicy.Parse(rev.PreviousCanonical)
	if err != nil {
		return j, err
	}
	if j.PolicyDigest == digest && j.PolicyAuthority.AuthorityID == binding.AuthorityID {
		if j.SchemaVersion != 4 || !bytes.Equal(j.PolicyHistory[rev.PreviousDigest], rev.PreviousCanonical) {
			return j, errors.New("revision target lost its previous canonical policy")
		}
		return j, validateTransitionRecords(j, policy)
	}
	if j.PolicyDigest != rev.PreviousDigest || j.PolicyAuthority.AuthorityID != rev.PreviousAuthorityID {
		return j, errors.New("revision source journal differs from previous authority")
	}
	if err := validateTransitionRecords(j, old); err != nil {
		return j, err
	}
	for _, rec := range j.Records {
		if rec.Kind == "pending" {
			return j, errors.New("pending transition blocks policy revision")
		}
	}
	// Never mutate the source map while preparing a target that may be rejected.
	history := make(map[string][]byte, len(j.PolicyHistory)+2)
	for key, value := range j.PolicyHistory {
		history[key] = bytes.Clone(value)
	}
	history[rev.PreviousDigest], history[digest] = bytes.Clone(rev.PreviousCanonical), canonical
	j.SchemaVersion, j.PolicyDigest, j.PolicyAuthority, j.PolicyHistory = 4, digest, &binding, history
	return j, validateTransitionRecords(j, policy)
}

func preparePolicyRevision(r *os.Root, policy boardpolicy.Policy, binding policyAuthorityBinding, head string, options PolicyRevisionOptions) (policyActivationState, error) {
	j, err := loadTransitions(r)
	if err != nil {
		return policyActivationState{}, err
	}
	old, err := loadPolicyActivation(r)
	if err != nil {
		return policyActivationState{}, err
	}
	if old.Phase != "completed" || old.AuthorityID != options.ExpectedAuthorityID || old.Digest != options.ExpectedDigest || old.Scope != binding.Scope || old.Namespace != binding.Namespace {
		return policyActivationState{}, errors.New("policy revision compare-and-swap binding mismatch")
	}
	rev := &policyRevision{PreviousAuthorityID: old.AuthorityID, PreviousDigest: old.Digest, PreviousCanonical: bytes.Clone(old.Canonical)}
	state, _, err := preparePolicyChangeJournal(r, policy, binding, head, j, rev)
	return state, err
}
