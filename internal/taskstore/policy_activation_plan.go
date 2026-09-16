package taskstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// Preparation runs under common -> board locks and writes nothing. It accepts
// target parking directories without granting the new policy to normal callers.
func preparePolicyActivation(r *os.Root, policy boardpolicy.Policy, binding policyAuthorityBinding, head string) (policyActivationState, []byte, error) {
	j, err := loadTransitions(r)
	if err != nil {
		return policyActivationState{}, nil, err
	}
	return preparePolicyActivationJournal(r, policy, binding, head, j)
}

func preparePolicyActivationJournal(r *os.Root, policy boardpolicy.Policy, binding policyAuthorityBinding, head string, j transitionJournal) (policyActivationState, []byte, error) {
	var state policyActivationState
	if err := checkStorageGate(r, j); err != nil {
		return state, nil, err
	}
	if err := validatePolicyAuthority(binding); err != nil {
		return state, nil, err
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return state, nil, err
	}
	digest := bytesDigest(canonical)
	if j.SchemaVersion >= 2 && j.PolicyDigest != digest {
		return state, nil, errors.New("initial policy is immutable; different policy cannot be activated")
	}
	snapshot, err := policyActivationSnapshot(r, policy, j)
	if err != nil {
		return state, nil, err
	}
	root, err := filepath.EvalSymlinks(r.Name())
	if err != nil {
		return state, nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return state, nil, err
	}
	if err := verifyBoardHandle(r, root); err != nil {
		return state, nil, err
	}
	ids, err := boundedSnapshotFile(r, idsFile, maxIDsBytes)
	if err != nil {
		return state, nil, err
	}
	plan := policyActivationPlan{Root: root, HEAD: head, Snapshot: snapshot, OriginalIDs: bytesDigest(ids), TargetIDs: bytesDigest(ids), IDTarget: []byte{}}
	for _, item := range []struct {
		name   string
		limit  int
		target *string
	}{
		{transitionsFile, maxTransitionBytes, &plan.OriginalJournal},
		{policyFile, 64 << 10, &plan.OriginalPolicy},
		{policyActivationFile, maxPolicyActivationBytes, &plan.OriginalActivation},
	} {
		raw, err := boundedSnapshotFile(r, item.name, item.limit)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return state, nil, err
		}
		*item.target = bytesDigest(raw)
	}
	if j.SchemaVersion < 3 {
		j.SchemaVersion = 3
	}
	j.PolicyDigest, j.PolicyAuthority = digest, &binding
	target, err := encodeTransitionJournal(j, policy)
	if err != nil {
		return state, nil, err
	}
	plan.TargetJournal = bytesDigest(target)
	state = policyActivationState{SchemaVersion: 1, Phase: "pending", AuthorityID: binding.AuthorityID, Scope: binding.Scope, Namespace: binding.Namespace, Canonical: canonical, Digest: digest, Plan: plan}
	if _, err := policyActivationBytes(state); err != nil {
		return policyActivationState{}, nil, err
	}
	completed := state
	completed.Phase = "completed"
	if _, err := policyActivationBytes(completed); err != nil {
		return policyActivationState{}, nil, err
	}
	return state, target, nil
}

// Recovery derives target bytes only from a journal whose exact saved hash is
// present. It never creates a new authority, takes a new snapshot, or repairs an
// unrelated journal. Policy/local receipt checks belong to the transaction.
func reconstructPolicyTarget(r *os.Root, state policyActivationState) (transitionJournal, []byte, error) {
	if err := validatePolicyActivationState(state); err != nil {
		return transitionJournal{}, nil, err
	}
	policy, err := boardpolicy.Parse(state.Canonical)
	if err != nil {
		return transitionJournal{}, nil, err
	}
	raw, err := boundedSnapshotFile(r, transitionsFile, maxTransitionBytes)
	currentHash := ""
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return transitionJournal{}, nil, err
	}
	if err == nil {
		currentHash = bytesDigest(raw)
	}
	if currentHash != state.Plan.OriginalJournal && currentHash != state.Plan.TargetJournal {
		return transitionJournal{}, nil, errors.New("policy activation journal changed; preserve and restore the exact original or target")
	}
	j := transitionJournal{SchemaVersion: 1, Records: []transitionRecord{}}
	if currentHash != "" {
		j, err = decodeTransitionJournal(raw)
		if err != nil {
			return transitionJournal{}, nil, err
		}
	}
	if j.SchemaVersion >= 2 && j.PolicyDigest != state.Digest {
		return transitionJournal{}, nil, errors.New("policy activation original journal binds another policy")
	}
	if err := checkStorageGate(r, j); err != nil {
		return transitionJournal{}, nil, err
	}
	if err := validateTransitionRecords(j, policy); err != nil {
		return transitionJournal{}, nil, err
	}
	for _, record := range j.Records {
		if record.Kind == "pending" {
			return transitionJournal{}, nil, errors.New("pending transition cannot be activated")
		}
	}
	if j.SchemaVersion < 3 {
		j.SchemaVersion = 3
	}
	j.PolicyDigest = state.Digest
	j.PolicyAuthority = &policyAuthorityBinding{AuthorityID: state.AuthorityID, Scope: state.Scope, Namespace: state.Namespace}
	target, err := encodeTransitionJournal(j, policy)
	if err != nil {
		return transitionJournal{}, nil, err
	}
	if bytesDigest(target) != state.Plan.TargetJournal {
		return transitionJournal{}, nil, errors.New("policy activation target journal hash mismatch")
	}
	return j, target, nil
}
