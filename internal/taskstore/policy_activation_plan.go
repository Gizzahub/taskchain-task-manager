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
	return preparePolicyChangeJournal(r, policy, binding, head, j, nil)
}

func preparePolicyChangeJournal(r *os.Root, policy boardpolicy.Policy, binding policyAuthorityBinding, head string, j transitionJournal, revision *policyRevision) (policyActivationState, []byte, error) {
	return preparePolicyChangeWithModules(r, policy, binding, head, j, revision, false)
}

func preparePolicyChangeWithModules(r *os.Root, policy boardpolicy.Policy, binding policyAuthorityBinding, head string, j transitionJournal, revision *policyRevision, adopt bool) (policyActivationState, []byte, error) {
	var state policyActivationState
	if j.StorageProtocol == 5 {
		if _, err := validateCompletedArchiveCapacity(r, j); err != nil {
			return state, nil, err
		}
		if err := checkStorageGateWithoutArchive(r, j); err != nil {
			return state, nil, err
		}
	} else if err := checkStorageGate(r, j); err != nil {
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
	if revision == nil && j.SchemaVersion >= 2 && j.PolicyDigest != digest {
		return state, nil, errors.New("initial policy is immutable; different policy cannot be activated")
	}
	if revision != nil {
		j, err = revisedPolicyJournal(j, policy, binding, *revision)
		if err != nil {
			return state, nil, err
		}
	}
	previous := boardpolicy.Default()
	if revision != nil {
		previous, err = boardpolicy.Parse(revision.PreviousCanonical)
		if err != nil {
			return state, nil, err
		}
	} else if j.PolicyAuthority != nil && j.PolicyDigest == digest {
		previous = policy // joining an already declared scope, not expanding it
	}
	if err := validateModuleScopeChange(previous, policy, adopt); err != nil {
		return state, nil, err
	}
	var adoption *moduleIDAdoption
	var idTarget []byte
	if adopt {
		adoption, idTarget, err = prepareModuleIDs(r, policy, j, binding)
		if err != nil {
			return state, nil, err
		}
	}
	archiveBinding, err := prepareArchiveActivationBinding(r, j, binding.Namespace)
	if err != nil {
		return state, nil, err
	}
	snapshot, err := policyActivationSnapshotForBinding(r, policy, j, adoption, archiveBinding)
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
	if err != nil && !(errors.Is(err, fs.ErrNotExist) && adoption != nil && len(adoption.Original) == 0) {
		return state, nil, err
	}
	plan := policyActivationPlan{Root: root, HEAD: head, Snapshot: snapshot, OriginalIDs: optionalPolicyHash(ids), TargetIDs: optionalPolicyHash(ids), IDTarget: []byte{}, ArchiveActivationBinding: archiveBinding}
	if adoption != nil {
		plan.ModuleAdoption, plan.IDTarget, plan.TargetIDs = adoption, idTarget, bytesDigest(idTarget)
	}
	for _, item := range []struct {
		name   string
		limit  int
		target *string
	}{
		{transitionsFile, maxTransitionBytes, &plan.OriginalJournal},
		{policyFile, 64 << 10, &plan.OriginalPolicy},
		{policyActivationFile, maxModuleActivationBytes, &plan.OriginalActivation},
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
	if revision != nil {
		state.SchemaVersion, state.Revision = 2, revision
	}
	if adoption != nil {
		state.SchemaVersion = 3
	}
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
	if j.StorageProtocol == 5 && state.Phase != "completed" && state.Plan.ArchiveActivationBinding == nil {
		return transitionJournal{}, nil, errors.New("protocol 5 pending policy activation lacks archive binding")
	}
	if state.Revision != nil {
		j, err = revisedPolicyJournal(j, policy, policyAuthorityBinding{AuthorityID: state.AuthorityID, Scope: state.Scope, Namespace: state.Namespace}, *state.Revision)
		if err != nil {
			return transitionJournal{}, nil, err
		}
	} else if j.SchemaVersion >= 2 && j.PolicyDigest != state.Digest {
		return transitionJournal{}, nil, errors.New("policy activation original journal binds another policy")
	}
	if state.Plan.ArchiveActivationBinding != nil {
		if err := checkStorageGateWithoutArchive(r, j); err != nil {
			return transitionJournal{}, nil, err
		}
	} else if err := checkStorageGate(r, j); err != nil {
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
