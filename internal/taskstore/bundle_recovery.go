package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func (s *bundleSession) validateResume(record bundleRecord) error {
	if s.transitions.BundleProtocol != 1 || !s.journalExists {
		return errors.New("bundle protocol adoption is incomplete")
	}
	if err := validateBundleRecord(record); err != nil {
		return err
	}
	if err := s.shared.verifyBundleOwner(s.root, s.journal, record); err != nil {
		return err
	}
	digest, err := s.policy.Digest()
	if err != nil {
		return err
	}
	if digest != record.PolicyDigest {
		return errors.New("bundle policy binding changed; restore its exact policy")
	}
	raw, err := boundedSnapshotFile(s.root, idsFile, maxIDsBytes)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, record.OriginalIDs) && !bytes.Equal(raw, record.TargetIDs) {
		return errors.New("bundle local ID ledger conflicts with original and target snapshots")
	}
	request, err := intentdoc.ParseBundle(record.Request)
	if err != nil {
		return err
	}
	req, err := request.Snapshot()
	if err != nil {
		return err
	}
	intent, found, err := readContext(s.root, "intent", req.Batch.Intent.ID, req.Batch.Intent.Revision)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("bundle registered Intent is missing; restore it")
	}
	intentRaw, err := intent.Canonical()
	if err != nil {
		return err
	}
	if !bytes.Equal(intentRaw, record.Intent) {
		return errors.New("bundle registered Intent changed")
	}
	batch, found, err := readContext(s.root, "batch", req.Batch.ID, req.Batch.Revision)
	if err != nil {
		return err
	}
	if found {
		batchRaw, err := batch.Canonical()
		if err != nil {
			return err
		}
		if !bytes.Equal(batchRaw, record.Batch) {
			return errors.New("bundle Batch destination contains different content")
		}
	}
	entries, err := listLockedWithPolicy(s.root, "", s.policy)
	if err != nil {
		return err
	}
	planned := map[string]bundleCard{}
	for _, item := range record.Cards {
		if err := validateBundleDestination(s.root, item.Path, s.policy); err != nil {
			return err
		}
		if _, err := bundleFileMatches(s.root, item.Path, item.Raw); err != nil {
			return err
		}
		planned[identityKey(item.ID)] = item
	}
	proposed := make([]Entry, 0, len(entries)+len(record.Cards))
	for _, entry := range entries {
		if item, exists := planned[identityKey(entry.Card.ID)]; exists {
			if entry.Path != item.Path {
				return fmt.Errorf("bundle ID exists at another path: %s", entry.Path)
			}
			continue
		}
		proposed = append(proposed, entry)
	}
	for _, item := range record.Cards {
		doc, err := card.Parse(item.Raw)
		if err != nil {
			return err
		}
		proposed = append(proposed, Entry{Path: item.Path, Card: doc.Snapshot(item.Path)})
	}
	if err := validateGraph(proposed); err != nil {
		return err
	}
	claims, err := loadClaims(s.root, proposed)
	if err != nil {
		return err
	}
	for _, claim := range claims.Records {
		if _, exists := planned[identityKey(claim.ID)]; exists {
			return errors.New("unpublished bundle ID has conflicting claim history")
		}
	}
	return nil
}

func bundleFileMatches(r *os.Root, path string, want []byte) (bool, error) {
	info, err := r.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return false, fmt.Errorf("bundle publication path has conflicting type or mode: %s", path)
	}
	raw, err := boundedSnapshotFile(r, path, maxPreparedBundleBytes)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(raw, want) {
		return false, fmt.Errorf("bundle publication content conflicts: %s", path)
	}
	return true, nil
}

func publishBundleFile(r *os.Root, path string, raw []byte) error {
	found, err := bundleFileMatches(r, path, raw)
	if err != nil || found {
		return err
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	return errors.Join(r.Link(name, path), r.Remove(name))
}
