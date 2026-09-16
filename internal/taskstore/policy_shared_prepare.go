package taskstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

// Only explicit shared adoption can import a completed receipt from another
// owner. The caller has verified actual Git membership and holds both locks.
// Pending receipts and foreign shared authorities are never portable.
func policyJournalForSharedAdoption(r *os.Root, s *sharedSession, canonical []byte) (transitionJournal, error) {
	raw, err := boundedSnapshotFile(r, transitionsFile, maxTransitionBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return loadTransitions(r)
	}
	if err != nil {
		return transitionJournal{}, err
	}
	j, err := decodeTransitionJournal(raw)
	if err != nil {
		return j, err
	}
	if err := s.verifyStorageBinding(j); err != nil {
		return j, err
	}
	if j.SchemaVersion != 3 {
		return loadTransitions(r)
	}
	state, err := loadPolicyActivation(r)
	if err != nil {
		return j, err
	}
	binding := j.PolicyAuthority
	if state.Phase != "completed" || binding == nil || state.AuthorityID != binding.AuthorityID || state.Scope != binding.Scope || state.Namespace != binding.Namespace || state.Digest != j.PolicyDigest || !bytes.Equal(state.Canonical, canonical) {
		return j, errors.New("shared policy adoption requires matching completed original binding")
	}
	if binding.Scope == "shared" && (s.state.Policy == nil || binding.AuthorityID != s.state.Policy.AuthorityID || binding.Namespace != s.state.NamespaceID) {
		return j, errors.New("foreign or orphaned shared policy cannot be adopted")
	}
	policyRaw, err := boundedSnapshotFile(r, policyFile, 64<<10)
	if err != nil {
		return j, err
	}
	if !bytes.Equal(policyRaw, canonical) {
		return j, errors.New("shared policy adoption policy bytes mismatch")
	}
	policy, err := boardpolicy.Parse(canonical)
	if err != nil {
		return j, err
	}
	if err := validateTransitionRecords(j, policy); err != nil {
		return j, err
	}
	if err := checkBundleGate(r, j); err != nil {
		return j, err
	}
	if err := checkStorageGate(r, j); err != nil {
		return j, err
	}
	return j, nil
}

func prepareSharedPolicy(s *sharedSession, boards []sharedPolicyBoard, policy boardpolicy.Policy, phase string) (sharedState, error) {
	next := *s.state
	canonical, err := policy.Canonical()
	if err != nil {
		return next, err
	}
	id := ""
	if s.state.Policy != nil {
		id = s.state.Policy.AuthorityID
	} else {
		id, err = newPolicyAuthorityID()
		if err != nil {
			return next, err
		}
	}
	authority := sharedPolicyAuthority{AuthorityID: id, Phase: phase, Canonical: canonical, Digest: bytesDigest(canonical), Pending: []policyActivationPlan{}}
	binding := policyAuthorityBinding{AuthorityID: id, Scope: "shared", Namespace: s.state.NamespaceID}
	states := make([]policyActivationState, len(boards))
	ledgers := make([]idLedger, len(boards))
	for i, b := range boards {
		j, err := policyJournalForSharedAdoption(b.root, s, canonical)
		if err != nil {
			return next, err
		}
		state, _, err := preparePolicyActivationJournal(b.root, policy, binding, b.head, j)
		if err != nil {
			return next, err
		}
		states[i] = state
		ledger, err := loadIDs(b.root)
		if err != nil {
			return next, err
		}
		if ledger.SchemaVersion == 3 && ledger.Namespace != s.state.NamespaceID {
			return next, errors.New("policy join cannot replace a foreign ID namespace")
		}
		ledgers[i] = ledger
		entries, err := listLockedWithPolicy(b.root, "", policy)
		if err != nil {
			return next, err
		}
		claims, err := loadClaims(b.root, entries)
		if err != nil {
			return next, err
		}
		observed, err := observedIDsWithRecords(entries, ledger, nil, claims, j)
		if err != nil {
			return next, err
		}
		next.Reserved = unionIDs(next.Reserved, observed.Reserved)
	}
	history, err := githistory.Scan(context.Background(), s.location.Repository, s.location.Board)
	if err != nil {
		return next, err
	}
	next.Reserved = unionIDs(next.Reserved, history.IDs)
	for i, state := range states {
		if ledgers[i].SchemaVersion != 3 {
			target, err := ledgerBytes(idLedger{SchemaVersion: 3, Namespace: s.state.NamespaceID, Reserved: next.Reserved})
			if err != nil {
				return next, err
			}
			state.Plan.IDTarget = target
			state.Plan.TargetIDs = bytesDigest(target)
		}
		if _, err := policyActivationBytes(state); err != nil {
			return next, err
		}
		if _, _, err := inspectPolicyActivationPlan(boards[i].root, state); err != nil {
			return next, err
		}
		authority.Pending = append(authority.Pending, state.Plan)
	}
	next.SchemaVersion, next.Policy = 3, &authority
	if err := validateSharedState(next); err != nil {
		return next, err
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return next, err
	}
	if len(raw)+1 > maxSharedStateBytes {
		return next, errors.New("policy activation shared plan exceeds 16 MiB")
	}
	return next, nil
}

func sharedPolicyLocalState(s sharedState, plan policyActivationPlan) policyActivationState {
	return policyActivationState{SchemaVersion: 1, Phase: "pending", AuthorityID: s.Policy.AuthorityID, Scope: "shared", Namespace: s.NamespaceID, Canonical: s.Policy.Canonical, Digest: s.Policy.Digest, Plan: plan}
}
