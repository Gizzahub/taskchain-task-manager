package taskstore

import (
	"errors"
	"io"
	"os"
)

func (s *repairSession) verifyPendingRepair(rec repairRecord, req RepairRequest) error {
	if err := s.shared.verifyBoardIdentity(s.root); err != nil {
		return err
	}
	board, err := canonicalStorageBoard(s.root)
	if err != nil {
		return err
	}
	if rec.BoardPath != board {
		return errors.New("pending repair belongs to another board")
	}
	namespace := ""
	if s.shared != nil && s.shared.state != nil {
		namespace = s.shared.state.NamespaceID
	}
	if rec.Namespace != namespace {
		return errors.New("pending repair namespace changed")
	}
	if namespace != "" {
		p := s.shared.state.PendingRepair
		if p == nil || p.Owner != board || p.RequestID != req.RequestID {
			return errors.New("pending repair lost its common reservation")
		}
	}
	j, err := loadTransitionsForStorage(s.root)
	if err != nil {
		return err
	}
	if j.StorageProtocol != 1 {
		return errors.New("pending repair lost its protocol binding")
	}
	if err := checkBundleGate(s.root, j); err != nil {
		return err
	}
	for _, r := range j.Records {
		if r.Kind == "pending" {
			return errors.New("pending transition conflicts with repair")
		}
	}
	if err := s.shared.verifyPolicyAuthority(s.root, j); err != nil {
		return err
	}
	policy, err := policyForJournal(s.root, j)
	if err != nil {
		return err
	}
	digest, err := policy.Digest()
	if err != nil {
		return err
	}
	if digest != rec.PolicyDigest {
		return errors.New("pending repair policy changed")
	}
	entries, err := listLockedWithPolicy(s.root, "", policy)
	if err != nil {
		return err
	}
	claims, err := loadClaims(s.root, entries)
	if err != nil {
		return err
	}
	return validateRepairClaim(claims, req)
}

func (s *repairSession) finishRepair(rec repairRecord, req RepairRequest, step func(string) error) (RepairResult, error) {
	if err := s.verifyPendingRepair(rec, req); err != nil {
		return RepairResult{}, err
	}
	patched, err := inspectRepairFile(s.root, rec)
	if err != nil {
		return RepairResult{}, err
	}
	if !patched {
		name, err := stageWith(s.root, rec.Patched, func(f *os.File, raw []byte) error {
			n, err := f.Write(raw)
			if err != nil {
				return err
			}
			if n != len(raw) {
				return io.ErrShortWrite
			}
			if err := f.Chmod(os.FileMode(rec.Mode)); err != nil {
				return err
			}
			return f.Sync()
		})
		if err != nil {
			return RepairResult{}, err
		}
		if err := storageStep(step, "after-stage"); err != nil {
			return RepairResult{}, errors.Join(err, s.root.Remove(name))
		}
		if err := s.verifyPendingRepair(rec, req); err != nil {
			return RepairResult{}, errors.Join(err, s.root.Remove(name))
		}
		patched, err = inspectRepairFile(s.root, rec)
		if err != nil {
			return RepairResult{}, errors.Join(err, s.root.Remove(name))
		}
		if patched {
			if err := s.root.Remove(name); err != nil {
				return RepairResult{}, err
			}
		} else if err := s.root.Rename(name, rec.Path); err != nil {
			return RepairResult{}, errors.Join(err, s.root.Remove(name))
		}
	}
	if err := storageStep(step, "after-replacement"); err != nil {
		return RepairResult{}, err
	}
	if err := s.verifyPendingRepair(rec, req); err != nil {
		return RepairResult{}, err
	}
	patched, err = inspectRepairFile(s.root, rec)
	if err != nil {
		return RepairResult{}, err
	}
	if !patched {
		return RepairResult{}, errors.New("repair target reverted before completion")
	}
	if err := syncRoot(s.root); err != nil {
		return RepairResult{}, err
	}
	completed := completedRepairJournal(s.journal)
	if err := saveRepairJournal(s.root, completed, false); err != nil {
		return RepairResult{}, err
	}
	s.journal = completed
	if err := storageStep(step, "after-receipt"); err != nil {
		return RepairResult{}, err
	}
	if err := s.clearCommon(req); err != nil {
		return RepairResult{}, err
	}
	return repairResult(rec), nil
}
