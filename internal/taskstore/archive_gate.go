package taskstore

import (
	"errors"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type completedArchiveCapacity struct {
	adoption   archiveCapacityAdoption
	receiptRaw []byte
	original   []byte
	target     []byte
	mode       uint32
	live       []byte
	journal    archiveJournal
	board      string
}

// validateCompletedArchiveCapacity proves the permanent protocol-5 barrier,
// the exact receipt and its payload cross-binding, and the current live
// schema-2 archive. The payload describes historical conversion bytes only;
// later valid archive writes need not equal its target or retain its mode.
func validateCompletedArchiveCapacity(r *os.Root, transitions transitionJournal) (completedArchiveCapacity, error) {
	var out completedArchiveCapacity
	if transitions.StorageProtocol == 6 {
		return validateCompletedOwnerRejoinCapacity(r)
	}
	if transitions.StorageProtocol != 5 {
		return out, errors.New("completed archive capacity requires permanent protocol 5 barrier")
	}
	receiptRaw, err := boundedSnapshotFile(r, archiveCapacityFile, 4096)
	if err != nil {
		return out, errors.New("capacity-adopted archive journal is missing its adoption receipt; restore it")
	}
	a, err := loadArchiveCapacityAdoption(r)
	if err != nil || a.Phase != "completed" || a.StorageProtocol != 5 {
		return out, errors.New("archive capacity receipt is not completed protocol 5")
	}
	original, target, mode, err := loadArchiveCapacityPayload(r, a.PayloadSHA256)
	if err != nil || validateCapacityPayloadBinding(a, original, target, mode) != nil {
		return out, errors.New("archive capacity payload does not cross-bind receipt")
	}
	return completedArchiveCapacityTail(r, a, receiptRaw, original, target, mode)
}

// validateCompletedOwnerRejoinCapacity is the protocol-6 counterpart.  The
// receipt is the schema-2 rejoin receipt and its payload is the separately
// transported owner-rejoin capacity frame, whose source board binding comes
// only from the completed local rejoin plan - never from the receipt alone.
func validateCompletedOwnerRejoinCapacity(r *os.Root) (completedArchiveCapacity, error) {
	var out completedArchiveCapacity
	plan, err := ownerRejoinCompletedLocal(r)
	if err != nil {
		return out, err
	}
	receiptRaw, err := boundedSnapshotFile(r, archiveCapacityFile, 4096)
	if err != nil {
		return out, errors.New("rejoined archive journal is missing its adoption receipt; restore it")
	}
	a, err := loadArchiveCapacityAdoption(r)
	if err != nil || a.SchemaVersion != 2 || a.Phase != "completed" || a.StorageProtocol != 6 {
		return out, errors.New("archive capacity receipt is not a completed protocol 6 owner-rejoin receipt")
	}
	name := ownerRejoinCapacityArtifactPath(a.PayloadSHA256)
	raw, err := loadOwnerRejoinArtifact(r, name, maxOwnerRejoinCapacityArtifactBytes)
	if err != nil {
		return out, errors.New("owner-rejoin capacity payload is missing or unreadable; restore it")
	}
	original, target, mode, err := decodeOwnerRejoinCapacityPayload(raw, a.PayloadSHA256, plan.SourceOwner, plan.TargetOwner)
	if err != nil || validateCapacityPayloadBinding(a, original, target, mode) != nil {
		return out, errors.New("owner-rejoin capacity payload does not cross-bind receipt")
	}
	return completedArchiveCapacityTail(r, a, receiptRaw, original, target, mode)
}

// completedArchiveCapacityTail proves the live schema-2 archive for either
// barrier.  The payload describes historical conversion bytes only; later
// valid archive writes need not equal its target or retain its mode.
func completedArchiveCapacityTail(r *os.Root, a archiveCapacityAdoption, receiptRaw, original, target []byte, mode uint32) (completedArchiveCapacity, error) {
	var out completedArchiveCapacity
	board, err := canonicalStorageBoard(r)
	if err != nil || a.BoardPath != board {
		return out, errors.New("archive capacity receipt belongs to another board")
	}
	info, err := r.Lstat(archivesFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() == 0 {
		return out, errors.New("live archive type or mode invalid")
	}
	live, err := boundedSnapshotFile(r, archivesFile, maxArchiveCapacityBytes)
	if err != nil {
		return out, err
	}
	j, err := decodeArchiveCapacityJournal(live)
	if err != nil || j.SchemaVersion != 2 || j.BoardPath != board {
		return out, errors.New("capacity-adopted live archive is not scoped schema 2")
	}
	for _, rec := range j.Records {
		if rec.State != "completed" {
			return out, errors.New("capacity-adopted live archive has pending record")
		}
	}
	out = completedArchiveCapacity{adoption: a, receiptRaw: receiptRaw, original: original, target: target, mode: mode, live: live, journal: j, board: board}
	return out, nil
}

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
		capacity, err := validateCompletedArchiveCapacity(r, transitions)
		if err != nil {
			return nil, err
		}
		j := capacity.journal
		ids, err := loadIDs(r)
		if err != nil {
			return nil, err
		}
		if _, err := archiveCompletedBindings(j, capacity.board, ids.Namespace); err != nil {
			return nil, err
		}
		return &j, nil
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
