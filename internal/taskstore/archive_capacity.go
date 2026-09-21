package taskstore

import (
	"bytes"
	"errors"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

type ArchiveCapacityResult struct {
	SchemaVersion   int                      `json:"schemaVersion"`
	UpgradeID       string                   `json:"upgradeId"`
	Status          outputvocab.ResultStatus `json:"status"`
	StorageProtocol int                      `json:"storageProtocol"`
	JournalSchema   int                      `json:"journalSchema"`
}

func ArchiveCapacity(dir, upgradeID string, adopt, resume bool) (ArchiveCapacityResult, error) {
	return archiveCapacityWithStep(dir, upgradeID, adopt, resume, nil)
}

// AdoptArchiveCapacity remains the programmatic convenience for an initial
// adoption. Recovery callers and the CLI use ArchiveCapacity with --resume.
func AdoptArchiveCapacity(dir, upgradeID string) error {
	_, err := ArchiveCapacity(dir, upgradeID, true, false)
	return err
}

func archiveCapacityWithStep(dir, upgradeID string, adopt, resume bool, step func(string) error) (result ArchiveCapacityResult, err error) {
	if !sharedHex32.MatchString(upgradeID) {
		return result, errors.New("invalid archive capacity upgrade ID")
	}
	if adopt == resume {
		return result, errors.New("archive capacity requires exactly one of adopt or resume")
	}
	s, err := openArchiveCapacitySession(dir, upgradeID)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	if s.adoption == nil {
		if resume {
			return result, errors.New("archive capacity recovery journal not found; --resume never starts adoption")
		}
		if err := startArchiveCapacity(s, upgradeID, step); err != nil {
			return result, err
		}
	} else if adopt {
		return result, errors.New("archive capacity adoption already recorded; use --resume with the exact upgrade ID")
	}
	if err := resumeArchiveCapacity(s, step); err != nil {
		return result, err
	}
	return ArchiveCapacityResult{SchemaVersion: 1, UpgradeID: upgradeID, Status: outputvocab.Completed, StorageProtocol: 5, JournalSchema: 2}, nil
}

func startArchiveCapacity(s *archiveCapacitySession, upgradeID string, step func(string) error) error {
	targetJournal := s.journal
	targetJournal.SchemaVersion = 2
	target, err := archiveCapacityJournalBytes(targetJournal)
	if err != nil {
		return err
	}
	payload, err := publishArchiveCapacityPayload(s.root, s.raw, target, s.mode)
	if err != nil {
		return err
	}
	if err := storageStep(step, "after-capacity-payload"); err != nil {
		return err
	}
	a := archiveCapacityAdoption{SchemaVersion: 1, Phase: "pending", UpgradeID: upgradeID, BoardPath: s.board, Namespace: s.namespace, SourceJournalSchema: 1, TargetJournalSchema: 2, StorageProtocol: 5, JournalMode: s.mode, OriginalLength: len(s.raw), OriginalSHA256: bytesDigest(s.raw), TargetLength: len(target), TargetSHA256: bytesDigest(target), PayloadSHA256: payload}
	if err := saveArchiveCapacityAdoption(s.root, a, true); err != nil {
		return err
	}
	s.adoption, s.original, s.target = &a, append([]byte(nil), s.raw...), target
	return storageStep(step, "after-capacity-local-pending")
}

func resumeArchiveCapacity(s *archiveCapacitySession, step func(string) error) error {
	a := *s.adoption
	if s.shared != nil && s.shared.state != nil {
		p := s.shared.state.PendingArchiveCapacity
		if p == nil {
			if a.Phase == "pending" {
				next := *s.shared.state
				next.StorageProtocol = 5
				next.PendingArchiveCapacity = &archiveCapacityPending{Owner: s.board, UpgradeID: a.UpgradeID}
				if err := s.shared.saveStorageState(next); err != nil {
					return err
				}
			} else if s.shared.state.StorageProtocol != 5 {
				return errors.New("completed archive capacity adoption lost common protocol marker")
			}
		} else if p.Owner != s.board || p.UpgradeID != a.UpgradeID {
			return errors.New("shared archive capacity marker belongs to another adoption")
		}
	}
	if err := storageStep(step, "after-capacity-common-protocol"); err != nil {
		return err
	}
	if s.transitions.StorageProtocol < 5 {
		s.transitions.StorageProtocol = 5
		if err := publishTransitionJournal(s.root, s.transitions); err != nil {
			return err
		}
	}
	if err := storageStep(step, "after-capacity-local-protocol"); err != nil {
		return err
	}
	if bytes.Equal(s.raw, s.original) {
		if err := publishArchiveCapacityTarget(s.root, s.target, s.mode, a.TargetSHA256); err != nil {
			return err
		}
		s.raw = append([]byte(nil), s.target...)
	} else if err := verifyArchiveCapacityTarget(s.root, s.target, s.mode, a.TargetSHA256); err != nil {
		return err
	}
	if err := storageStep(step, "after-capacity-target"); err != nil {
		return err
	}
	if a.Phase == "pending" {
		a.Phase = "completed"
		if err := saveArchiveCapacityAdoption(s.root, a, false); err != nil {
			return err
		}
		s.adoption = &a
	}
	if err := storageStep(step, "after-capacity-completed"); err != nil {
		return err
	}
	if s.shared != nil && s.shared.state != nil && s.shared.state.PendingArchiveCapacity != nil {
		next := *s.shared.state
		next.PendingArchiveCapacity = nil
		if err := s.shared.saveStorageState(next); err != nil {
			return err
		}
	}
	return storageStep(step, "after-capacity-common-clear")
}
