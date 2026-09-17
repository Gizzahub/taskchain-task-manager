package taskstore

import (
	"bytes"
	"errors"
)

// The envelope shape is shared with repair, but its wire key and payload
// validator are distinct. A repair session cannot recover a relocation.
func validateSharedRelocation(s sharedState) error {
	p := s.PendingRelocation
	if p == nil {
		return nil
	}
	if (s.StorageProtocol < 2 || s.StorageProtocol > 5) || s.Phase != "active" || s.PendingRepair != nil || s.PendingBundle != nil || s.PendingArchive != nil || s.PendingArchiveDelta != nil || s.PendingArchiveCapacity != nil || (s.Policy != nil && s.Policy.Phase != "active") {
		return errors.New("conflicting shared relocation reservation")
	}
	if !validSharedRoot(p.Owner) || !sharedHex32.MatchString(p.RequestID) || !sharedHex64.MatchString(p.OriginalJournalSHA256) {
		return errors.New("invalid shared relocation binding")
	}
	j, err := decodeRelocationJournal(p.TargetJournal)
	if err != nil {
		return err
	}
	canonical, err := relocationJournalBytes(j)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, p.TargetJournal) || j.BoardPath != p.Owner {
		return errors.New("shared relocation target is not canonical or belongs to another board")
	}
	base := j
	base.Records = []relocationRecord{}
	found := false
	for _, rec := range j.Records {
		if rec.Kind != "pending" {
			base.Records = append(base.Records, rec)
			continue
		}
		if rec.RequestID != p.RequestID || rec.Namespace != s.NamespaceID {
			return errors.New("shared relocation request or namespace mismatch")
		}
		found = true
		reserved := false
		for _, id := range s.Reserved {
			if sameIdentity(id, rec.ID) {
				reserved = true
			}
		}
		if !reserved {
			return errors.New("shared relocation ID is not reserved")
		}
	}
	if !found {
		return errors.New("shared relocation has no pending record")
	}
	original, err := relocationJournalBytes(base)
	if err != nil {
		return err
	}
	if bytesDigest(original) != p.OriginalJournalSHA256 {
		return errors.New("shared relocation target does not preserve original receipts")
	}
	return nil
}
