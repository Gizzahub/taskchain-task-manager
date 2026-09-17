package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
)

func validateSharedArchiveShape(raw []byte) error {
	if err := validateSharedRepairShape(raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	value := bytes.TrimSpace(fields["targetJournal"])
	if len(value) == 0 || value[0] != '"' {
		return errors.New("shared archive target journal must be a base64 string")
	}
	return nil
}

// validateSharedArchive binds the shared reservation to one canonical archive
// journal. The pending record is intentionally treated like the other shared
// reservations: it must be the final record and its original receipt history
// must remain byte-for-byte identifiable by digest.
func validateSharedArchive(s sharedState) error {
	p := s.PendingArchive
	if p == nil {
		return nil
	}
	if s.StorageProtocol != 3 || s.Phase != "active" || s.PendingRepair != nil || s.PendingRelocation != nil || s.PendingBundle != nil || (s.Policy != nil && s.Policy.Phase != "active") {
		return errors.New("conflicting shared archive reservation")
	}
	if !validSharedRoot(p.Owner) || !sharedHex32.MatchString(p.RequestID) || !sharedHex64.MatchString(p.OriginalJournalSHA256) {
		return errors.New("invalid shared archive binding")
	}
	j, err := decodeArchiveJournal(p.TargetJournal)
	if err != nil {
		return err
	}
	canonical, err := archiveJournalBytes(j)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, p.TargetJournal) || j.BoardPath != p.Owner || j.Namespace != s.NamespaceID {
		return errors.New("shared archive target is not canonical or belongs to another namespace")
	}
	base := j
	base.Records = []archiveRecord{}
	found := false
	for _, rec := range j.Records {
		if rec.State != "pending" {
			base.Records = append(base.Records, rec)
			continue
		}
		if found || rec.RequestID != p.RequestID || rec.BoardPath != p.Owner || rec.Namespace != s.NamespaceID {
			return errors.New("shared archive request or namespace mismatch")
		}
		found = true
		reserved := false
		for _, id := range s.Reserved {
			if sameIdentity(id, rec.ID) {
				reserved = true
				break
			}
		}
		if !reserved {
			return errors.New("shared archive ID is not reserved")
		}
	}
	if !found {
		return errors.New("shared archive has no pending record")
	}
	original, err := archiveJournalBytes(base)
	if err != nil {
		return err
	}
	if bytesDigest(original) != p.OriginalJournalSHA256 {
		return errors.New("shared archive target does not preserve the original receipts")
	}
	return nil
}
