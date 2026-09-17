package taskstore

import (
	"errors"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func checkArchiveGate(r *os.Root, transitions transitionJournal) error {
	_, err := archiveForBoard(r, transitions)
	return err
}

// The caller already holds common then board locks. Absence means legacy
// semantics only when no archive protocol was adopted. Never recreate here.
func archiveForBoard(r *os.Root, transitions transitionJournal) (*archiveJournal, error) {
	adoption, adoptionErr := loadArchiveCapacityAdoption(r)
	if adoptionErr == nil {
		if adoption.Phase != "completed" {
			return nil, errors.New("pending archive capacity adoption requires exact recovery")
		}
		if transitions.StorageProtocol != 5 {
			return nil, errors.New("completed archive capacity adoption lost its protocol barrier; restore it")
		}
		if _, _, _, err := loadArchiveCapacityPayload(r, adoption.PayloadSHA256); err != nil {
			return nil, errors.New("archive capacity payload missing or invalid; restore it")
		}
	} else if !errors.Is(adoptionErr, fs.ErrNotExist) {
		return nil, adoptionErr
	} else if transitions.StorageProtocol >= 5 {
		return nil, errors.New("capacity-adopted archive journal is missing its adoption receipt; restore it")
	}
	j, err := loadArchiveJournalForProtocol(r, transitions.StorageProtocol)
	if errors.Is(err, fs.ErrNotExist) {
		if transitions.StorageProtocol < 3 {
			return nil, nil
		}
		return nil, errors.New("archive journal missing from adopted board; restore it")
	}
	if err != nil {
		return nil, err
	}
	if transitions.StorageProtocol < 3 || transitions.StorageProtocol > 5 {
		return nil, errors.New("archive adoption is incomplete; resume explicit adoption")
	}
	board, err := canonicalStorageBoard(r)
	if err != nil {
		return nil, err
	}
	if transitions.StorageProtocol >= 5 && j.SchemaVersion != 2 {
		return nil, errors.New("capacity-adopted archive journal is not schema 2; restore it")
	}
	ids, err := loadIDs(r)
	if err != nil {
		return nil, err
	}
	if _, err := archiveCompletedBindings(j, board, ids.Namespace); err != nil {
		return nil, err
	}
	return &j, nil
}

func completionForBoard(r *os.Root, entries []Entry, policy boardpolicy.Policy) (completionIndex, error) {
	transitions, err := loadTransitionsForStorage(r)
	if err != nil {
		return nil, err
	}
	j, err := archiveForBoard(r, transitions)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return completionForJournal(entries, policy, nil, "", "", nil)
	}
	return completionForJournal(entries, policy, j, j.BoardPath, j.Namespace, func(entry Entry) ([]byte, error) {
		return readTransitionCard(r, entry.Path)
	})
}

func readyForBoard(r *os.Root, entries []Entry, ledger claimsLedger, policy boardpolicy.Policy) ([]Entry, error) {
	completion, err := completionForBoard(r, entries, policy)
	if err != nil {
		return nil, err
	}
	return readyWithCompletion(entries, ledger, policy, completion), nil
}
