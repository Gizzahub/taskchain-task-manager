package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func (s *sharedSession) verifyBundleOwner(r *os.Root, journal bundleJournal, record bundleRecord) error {
	if err := validateBundleRecord(record); err != nil {
		return err
	}
	if s == nil {
		if record.Namespace != "" || record.Owner != "" {
			return errors.New("local bundle has a shared namespace binding")
		}
		return nil
	}
	owner := filepath.Clean(filepath.Join(s.location.Repository, filepath.FromSlash(s.location.Board)))
	if err := s.verifyBoardIdentity(r); err != nil {
		return err
	}
	if s.state == nil {
		if record.Namespace != "" || record.Owner != "" {
			return errors.New("bundle namespace binding has no active shared state")
		}
		return nil
	}
	if record.Namespace != s.state.NamespaceID {
		return errors.New("bundle namespace does not match active shared namespace")
	}
	if record.Owner == "" || filepath.Clean(record.Owner) != owner {
		return errors.New("bundle owner does not match active board")
	}
	if journal.BoardID == "" {
		return errors.New("bundle journal board ID is empty")
	}
	ids, err := bundleCardIDs(record)
	if err != nil {
		return err
	}
	if p := s.state.PendingBundle; p != nil {
		if p.RequestID != record.RequestID || p.Digest != record.RequestDigest || p.BoardID != journal.BoardID || filepath.Clean(p.Owner) != owner || !sameStringSlice(p.IDs, ids) {
			return errors.New("bundle pending receipt belongs to another owner or request")
		}
	}
	return nil
}

func (s *sharedSession) reserveBundle(r *os.Root, journal bundleJournal, record bundleRecord) error {
	if record.Status != "pending" {
		return errors.New("bundle reservation requires a pending record")
	}
	if err := s.verifyBundleOwner(r, journal, record); err != nil {
		return err
	}
	if s == nil || s.state == nil {
		return nil
	}
	if (s.state.SchemaVersion != 2 && s.state.SchemaVersion != 3) || s.state.BundleProtocol != 1 {
		return errors.New("bundle shared state requires explicit adoption before reservation")
	}
	if p := s.state.PendingBundle; p != nil {
		return nil
	}
	next, err := s.prepareBundleReservation(r, journal, record)
	if err != nil {
		return err
	}
	if next == nil {
		return nil
	}
	if err := publishSharedState(s.root, *next, false); err != nil {
		return err
	}
	s.state = next
	return nil
}

// prepareBundleReservation performs all shared ownership, collision and
// capacity checks without writing state. Local sessions return nil. A legacy
// active shared state is upgraded in the returned value only; reserveBundle
// requires explicit adoption before publishing that value.
func (s *sharedSession) prepareBundleReservation(r *os.Root, journal bundleJournal, record bundleRecord) (*sharedState, error) {
	if record.Status != "pending" {
		return nil, errors.New("bundle reservation requires a pending record")
	}
	if err := s.verifyBundleOwner(r, journal, record); err != nil {
		return nil, err
	}
	if s == nil || s.state == nil {
		return nil, nil
	}
	if s.state.PendingBundle != nil {
		next := *s.state
		return &next, nil
	}
	ids, err := bundleCardIDs(record)
	if err != nil {
		return nil, err
	}
	reserved := map[string]bool{}
	for _, id := range s.state.Reserved {
		reserved[id] = true
	}
	for _, id := range ids {
		if reserved[id] {
			return nil, fmt.Errorf("bundle card ID is already reserved: %s", id)
		}
	}
	next := *s.state
	if next.SchemaVersion != 3 {
		next.SchemaVersion = 2
	}
	next.BundleProtocol = 1
	target, err := decodeIDs(record.TargetIDs)
	if err != nil {
		return nil, fmt.Errorf("bundle target IDs: %w", err)
	}
	if target.SchemaVersion == 3 && target.Namespace != s.state.NamespaceID {
		return nil, errors.New("bundle target namespace does not match active shared namespace")
	}
	next.Reserved = unionIDs(next.Reserved, target.Reserved, ids)
	owner := filepath.Clean(filepath.Join(s.location.Repository, filepath.FromSlash(s.location.Board)))
	next.PendingBundle = &sharedBundlePending{RequestID: record.RequestID, Digest: record.RequestDigest, BoardID: journal.BoardID, Owner: owner, IDs: ids}
	if err := validateSharedState(next); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(append(raw, '\n')) > maxSharedStateBytes {
		return nil, errors.New("shared state exceeds 16 MiB")
	}
	return &next, nil
}

func (s *sharedSession) finishBundle(r *os.Root, journal bundleJournal, record bundleRecord) error {
	if record.Status != "completed" {
		return errors.New("bundle completion requires a completed record")
	}
	if err := s.verifyBundleOwner(r, journal, record); err != nil {
		return err
	}
	if s == nil || s.state == nil {
		return nil
	}
	ids, err := bundleCardIDs(record)
	if err != nil {
		return err
	}
	next := *s.state
	if p := next.PendingBundle; p != nil {
		if p.RequestID != record.RequestID || p.Digest != record.RequestDigest || p.BoardID != journal.BoardID || !sameStringSlice(p.IDs, ids) {
			return errors.New("bundle pending receipt belongs to another request")
		}
		next.PendingBundle = nil
	} else {
		reserved := map[string]bool{}
		for _, id := range next.Reserved {
			reserved[id] = true
		}
		for _, id := range ids {
			if !reserved[id] {
				return fmt.Errorf("completed bundle ID is not reserved: %s", id)
			}
		}
		return nil
	}
	if err := publishSharedState(s.root, next, false); err != nil {
		return err
	}
	s.state = &next
	return nil
}

func bundleCardIDs(record bundleRecord) ([]string, error) {
	ids := make([]string, 0, len(record.Cards))
	seen := map[string]bool{}
	for _, card := range record.Cards {
		id := identityKey(card.ID)
		if id == "" || seen[id] {
			return nil, errors.New("bundle cards require unique canonical TASK identities")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
