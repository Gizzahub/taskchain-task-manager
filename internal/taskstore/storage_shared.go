package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
)

// The common reservation contains the exact local pending journal. A crash
// before local publication must still be recoverable by the owning request.
type sharedRepairPending struct {
	Owner                 string `json:"owner"`
	RequestID             string `json:"requestId"`
	OriginalJournalSHA256 string `json:"originalJournalSha256"`
	TargetJournal         []byte `json:"targetJournal"`
}

func validateSharedRepairShape(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 4 {
		return errors.New("invalid shared repair shape")
	}
	for _, key := range []string{"owner", "requestId", "originalJournalSha256", "targetJournal"} {
		if fields[key] == nil || string(fields[key]) == "null" {
			return errors.New("shared repair requires all fields")
		}
	}
	return nil
}

func validateSharedStorage(s sharedState) error {
	if s.StorageProtocol < 0 || s.StorageProtocol > 4 {
		return errors.New("invalid shared storage protocol")
	}
	if err := validateSharedRelocation(s); err != nil {
		return err
	}
	p := s.PendingRepair
	if p == nil {
		return nil
	}
	if s.StorageProtocol < 1 || s.Phase != "active" || s.PendingBundle != nil || s.PendingRelocation != nil || s.PendingArchive != nil || s.PendingArchiveDelta != nil || (s.Policy != nil && s.Policy.Phase != "active") {
		return errors.New("conflicting shared storage reservation")
	}
	if !validSharedRoot(p.Owner) || !sharedHex32.MatchString(p.RequestID) || !sharedHex64.MatchString(p.OriginalJournalSHA256) {
		return errors.New("invalid shared repair binding")
	}
	j, err := decodeRepairJournal(p.TargetJournal)
	if err != nil {
		return err
	}
	canonical, err := repairJournalBytes(j)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, p.TargetJournal) {
		return errors.New("shared repair target journal is not canonical")
	}
	if j.BoardPath != p.Owner {
		return errors.New("shared repair owner mismatch")
	}
	found := false
	base := j
	base.Records = []repairRecord{}
	for _, rec := range j.Records {
		if rec.Kind == "pending" {
			if rec.RequestID != p.RequestID || rec.Namespace != s.NamespaceID {
				return errors.New("shared repair request or namespace mismatch")
			}
			found = true
			reserved := false
			for _, id := range s.Reserved {
				if sameIdentity(id, rec.ID) {
					reserved = true
				}
			}
			if !reserved {
				return errors.New("shared repair ID is not reserved")
			}
		} else {
			base.Records = append(base.Records, rec)
		}
	}
	if !found {
		return errors.New("shared repair has no pending record")
	}
	original, err := repairJournalBytes(base)
	if err != nil {
		return err
	}
	if bytesDigest(original) != p.OriginalJournalSHA256 {
		return errors.New("shared repair target does not preserve the original receipts")
	}
	return nil
}

func (s *sharedSession) saveStorageState(next sharedState) error {
	if err := s.verify(); err != nil {
		return err
	}
	if err := publishSharedState(s.root, next, false); err != nil {
		return err
	}
	s.state = &next
	return nil
}

func (s *sharedSession) adoptStorageProtocol() error {
	if s == nil || s.state == nil || s.state.StorageProtocol >= 1 {
		return nil
	}
	next := *s.state
	next.StorageProtocol = 1
	return s.saveStorageState(next)
}

func (s *sharedSession) verifyStorageBinding(j transitionJournal) error {
	if s != nil && s.state != nil && j.StorageProtocol > s.state.StorageProtocol {
		return errors.New("storage-adopted board lost its common protocol; restore common state")
	}
	return nil
}
