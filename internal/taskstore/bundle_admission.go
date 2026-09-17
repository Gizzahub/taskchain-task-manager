package taskstore

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

// prepareRecord reads a locked board and returns a reproducible transaction,
// but does not adopt a format, reserve an ID, or publish any card.
func (s *bundleSession) prepareRecord(document intentdoc.BundleDocument) (bundleRecord, error) {
	for _, record := range s.journal.Records {
		if record.Status == "pending" {
			return bundleRecord{}, errors.New("pending bundle requires exact request recovery")
		}
	}
	if s.shared != nil && s.shared.state != nil && s.shared.state.PendingBundle != nil {
		return bundleRecord{}, errors.New("shared pending bundle requires recovery from its owner")
	}
	req, err := document.Snapshot()
	if err != nil {
		return bundleRecord{}, err
	}
	entries, err := listLockedWithPolicy(s.root, "", s.policy)
	if err != nil {
		return bundleRecord{}, err
	}
	claims, err := loadClaims(s.root, entries)
	if err != nil {
		return bundleRecord{}, err
	}
	intent, found, err := readContext(s.root, "intent", req.Batch.Intent.ID, req.Batch.Intent.Revision)
	if err != nil {
		return bundleRecord{}, err
	}
	if !found {
		return bundleRecord{}, errors.New("bundle Intent revision is not registered")
	}
	if _, found, err := readContext(s.root, "batch", req.Batch.ID, req.Batch.Revision); err != nil || found {
		if err != nil {
			return bundleRecord{}, err
		}
		return bundleRecord{}, errors.New("bundle Batch key is already registered")
	}
	originalRaw, err := boundedSnapshotFile(s.root, idsFile, maxIDsBytes)
	if err != nil {
		return bundleRecord{}, err
	}
	ledger, err := decodeIDs(originalRaw)
	if err != nil {
		return bundleRecord{}, err
	}
	ledger, err = observedIDsWithRecords(entries, ledger, nil, claims, s.transitions)
	if err != nil {
		return bundleRecord{}, err
	}
	ledger, err = s.shared.merge(ledger)
	if err != nil {
		return bundleRecord{}, err
	}
	prepared, err := prepareTaskBundle(document, entries, ledger, intent)
	if err != nil {
		return bundleRecord{}, err
	}
	record := bundleRecord{Status: "pending", RequestID: prepared.RequestID, RequestDigest: prepared.RequestDigest,
		OriginalIDs: originalRaw, Namespace: ledger.Namespace, Cards: []bundleCard{}}
	if record.Namespace != "" {
		record.Owner = filepath.Join(s.shared.location.Repository, filepath.FromSlash(s.shared.location.Board))
	}
	record.Request, err = document.Canonical()
	if err != nil {
		return bundleRecord{}, err
	}
	record.Intent, err = intent.Canonical()
	if err != nil {
		return bundleRecord{}, err
	}
	record.BaseIDs, err = ledgerBytes(ledger)
	if err != nil {
		return bundleRecord{}, err
	}
	record.TargetIDs, err = ledgerBytes(prepared.Ledger)
	if err != nil {
		return bundleRecord{}, err
	}
	record.PolicyDigest, err = s.policy.Digest()
	if err != nil {
		return bundleRecord{}, err
	}
	record.Batch, err = prepared.Batch.Canonical()
	if err != nil {
		return bundleRecord{}, err
	}
	for i, item := range prepared.Cards {
		if err := validateBundleDestination(s.root, item.Entry.Path, s.policy); err != nil {
			return bundleRecord{}, err
		}
		if _, err := s.root.Lstat(item.Entry.Path); !errors.Is(err, fs.ErrNotExist) {
			if err != nil {
				return bundleRecord{}, err
			}
			return bundleRecord{}, fmt.Errorf("bundle card destination exists: %s", item.Entry.Path)
		}
		record.Cards = append(record.Cards, bundleCard{Key: req.Tasks[i].Key, ID: item.Entry.Card.ID, Path: item.Entry.Path, Raw: item.Raw})
	}
	next := s.journal
	next.Records = append(append([]bundleRecord(nil), s.journal.Records...), record)
	if _, err := bundleJournalBytes(next); err != nil {
		return bundleRecord{}, err
	}
	// Completion must fit too; a pending receipt cannot consume its own
	// completion headroom and become permanently unrecoverable.
	next.Records[len(next.Records)-1].Status = "completed"
	if _, err := bundleJournalBytes(next); err != nil {
		return bundleRecord{}, err
	}
	if _, err := s.shared.prepareBundleReservation(s.root, s.journal, record); err != nil {
		return bundleRecord{}, err
	}
	return record, nil
}
