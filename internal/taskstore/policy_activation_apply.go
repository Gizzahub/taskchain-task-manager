package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type policyActivationFiles struct {
	journal      []byte
	policy       []byte
	activation   []byte
	ids          []byte
	completed    bool
	idsPublished bool
}

// The caller holds common -> board locks and the durable transaction plan.
// Each resumed publication uses the saved plan; conflicting state is never
// repaired or replanned. Shared activation publishes its common pending first.
func applyPolicyActivationPlan(r *os.Root, state policyActivationState, step func(string) error) error {
	files, target, err := inspectPolicyActivationPlan(r, state)
	if err != nil {
		return err
	}
	if files.completed {
		return nil
	}
	pending := state
	pending.Phase = "pending"
	pendingRaw, err := policyActivationBytes(pending)
	if err != nil {
		return err
	}
	if !files.idsPublished && !bytes.Equal(files.activation, pendingRaw) {
		if err := savePolicyActivation(r, pending, files.activation == nil); err != nil {
			return err
		}
	}
	if err := bundleStep(step, "after-local-pending"); err != nil {
		return err
	}
	files, target, err = inspectPolicyActivationPlan(r, state)
	if err != nil {
		return err
	}
	if !bytes.Equal(files.policy, state.Canonical) {
		// A bound policy is immutable and preparation requires canonical bytes.
		// Thus only an absent initial policy can need publication here.
		if files.policy != nil && state.Revision == nil {
			return errors.New("policy activation cannot overwrite an existing policy")
		}
		if err := publishPolicyActivationFile(r, policyFile, state.Canonical, files.policy == nil); err != nil {
			return err
		}
	}
	if err := bundleStep(step, "after-policy"); err != nil {
		return err
	}
	files, target, err = inspectPolicyActivationPlan(r, state)
	if err != nil {
		return err
	}
	if bytesDigest(files.ids) != state.Plan.TargetIDs {
		if err := publishPolicyActivationFile(r, idsFile, state.Plan.IDTarget, files.ids == nil); err != nil {
			return err
		}
	}
	if state.Plan.ModuleAdoption != nil && !files.idsPublished {
		published := state
		published.Phase = "ids-published"
		if err := savePolicyActivation(r, published, false); err != nil {
			return err
		}
	}
	if err := bundleStep(step, "after-policy-ids"); err != nil {
		return err
	}
	files, target, err = inspectPolicyActivationPlan(r, state)
	if err != nil {
		return err
	}
	if !bytes.Equal(files.journal, target) {
		if err := publishPolicyActivationFile(r, transitionsFile, target, files.journal == nil); err != nil {
			return err
		}
	}
	if err := bundleStep(step, "after-policy-journal"); err != nil {
		return err
	}
	if _, _, err := inspectPolicyActivationPlan(r, state); err != nil {
		return err
	}
	completed := state
	completed.Phase = "completed"
	if err := savePolicyActivation(r, completed, false); err != nil {
		return err
	}
	return bundleStep(step, "after-local-completed")
}

func inspectPolicyActivationPlan(r *os.Root, state policyActivationState) (policyActivationFiles, []byte, error) {
	var files policyActivationFiles
	if err := verifyBoardHandle(r, state.Plan.Root); err != nil {
		return files, nil, err
	}
	j, target, err := reconstructPolicyTarget(r, state)
	if err != nil {
		return files, nil, err
	}
	policy, err := boardpolicy.Parse(state.Canonical)
	if err != nil {
		return files, nil, err
	}
	snapshot, err := policyActivationSnapshotForAdoption(r, policy, j, state.Plan.ModuleAdoption)
	if err != nil {
		return files, nil, err
	}
	if snapshot != state.Plan.Snapshot {
		return files, nil, errors.New("policy activation board snapshot changed; preserve original plan")
	}
	if err := validateModuleInventory(r, policy, j, state.Plan); err != nil {
		return files, nil, err
	}
	for _, item := range []struct {
		name  string
		limit int
		out   *[]byte
	}{
		{transitionsFile, maxTransitionBytes, &files.journal},
		{policyFile, 64 << 10, &files.policy},
		{policyActivationFile, maxModuleActivationBytes, &files.activation},
		{idsFile, maxIDsBytes, &files.ids},
	} {
		raw, err := boundedSnapshotFile(r, item.name, item.limit)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return files, nil, err
		}
		*item.out = raw
	}
	pending := state
	pending.Phase = "pending"
	pendingRaw, err := policyActivationBytes(pending)
	if err != nil {
		return files, nil, err
	}
	completed := state
	completed.Phase = "completed"
	completedRaw, err := policyActivationBytes(completed)
	if err != nil {
		return files, nil, err
	}
	files.completed = bytes.Equal(files.activation, completedRaw)
	if state.Plan.ModuleAdoption != nil {
		published := state
		published.Phase = "ids-published"
		publishedRaw, err := policyActivationBytes(published)
		if err != nil {
			return files, nil, err
		}
		files.idsPublished = bytes.Equal(files.activation, publishedRaw)
	}
	newReceipt := files.completed || files.idsPublished || bytes.Equal(files.activation, pendingRaw)
	idsHash := optionalPolicyHash(files.ids)
	if idsHash != state.Plan.OriginalIDs && idsHash != state.Plan.TargetIDs {
		return files, nil, errors.New("policy activation ID ledger changed; restore exact original or target")
	}
	if files.idsPublished && idsHash != state.Plan.TargetIDs {
		return files, nil, errors.New("published module ID ledger disappeared or changed; restore exact target")
	}
	if idsHash != state.Plan.OriginalIDs && !newReceipt {
		return files, nil, errors.New("ID target was published without its activation receipt")
	}
	if !newReceipt && optionalPolicyHash(files.activation) != state.Plan.OriginalActivation {
		return files, nil, errors.New("policy activation receipt changed; restore exact original or transaction receipt")
	}
	if !bytes.Equal(files.policy, state.Canonical) && optionalPolicyHash(files.policy) != state.Plan.OriginalPolicy {
		return files, nil, errors.New("policy activation policy file changed")
	}
	journalIsTarget := bytes.Equal(files.journal, target)
	journalWasReplaced := journalIsTarget && state.Plan.OriginalJournal != state.Plan.TargetJournal
	if journalWasReplaced && (!newReceipt || !bytes.Equal(files.policy, state.Canonical)) {
		return files, nil, errors.New("policy activation target lost its policy or receipt; restore missing state")
	}
	if (journalWasReplaced || files.completed) && idsHash != state.Plan.TargetIDs {
		return files, nil, errors.New("policy activation target journal lost its target ID ledger")
	}
	if files.completed && !journalIsTarget {
		return files, nil, errors.New("completed activation lost its target journal")
	}
	if !newReceipt && optionalPolicyHash(files.policy) != state.Plan.OriginalPolicy {
		return files, nil, errors.New("policy was published without its activation receipt; restore receipt")
	}
	return files, target, nil
}

func optionalPolicyHash(raw []byte) string {
	if raw == nil {
		return ""
	}
	return bytesDigest(raw)
}

func publishPolicyActivationFile(r *os.Root, name string, raw []byte, initial bool) error {
	staged, err := stage(r, raw)
	if err != nil {
		return err
	}
	if name == idsFile {
		info, err := r.Lstat(idsFile)
		mode := os.FileMode(0644)
		if err != nil && !(initial && errors.Is(err, fs.ErrNotExist)) {
			return errors.Join(err, r.Remove(staged))
		}
		if err == nil {
			if initial || !info.Mode().IsRegular() || info.Mode()&^os.ModePerm != 0 {
				return errors.Join(errors.New("unsafe or unexpected ID ledger"), r.Remove(staged))
			}
			mode = info.Mode().Perm()
		}
		if err := r.Chmod(staged, mode); err != nil {
			return errors.Join(err, r.Remove(staged))
		}
		file, err := r.Open(staged)
		if err != nil {
			return errors.Join(err, r.Remove(staged))
		}
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			return errors.Join(err, r.Remove(staged))
		}
	}
	if initial {
		if err := r.Link(staged, name); err != nil {
			return errors.Join(err, r.Remove(staged))
		}
		if err := r.Remove(staged); err != nil {
			return err
		}
	} else if err := r.Rename(staged, name); err != nil {
		return errors.Join(err, r.Remove(staged))
	}
	dir, err := r.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
