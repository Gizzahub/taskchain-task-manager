package taskstore

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

// validateBundleRecord checks stored intent, allocation and rendering evidence,
// not today's board graph. A completed receipt remains historical evidence.
func validateBundleRecord(record bundleRecord) error {
	if !sharedHex64.MatchString(record.PolicyDigest) || (record.Namespace != "" && !sharedHex32.MatchString(record.Namespace)) {
		return errors.New("invalid bundle policy or namespace binding")
	}
	if (record.Namespace == "" && record.Owner != "") || (record.Namespace != "" && !validSharedRoot(record.Owner)) {
		return errors.New("invalid bundle owner binding")
	}
	document, err := intentdoc.ParseBundle(record.Request)
	if err != nil {
		return err
	}
	canonical, _ := document.Canonical()
	digest, _ := document.Digest()
	req, _ := document.Snapshot()
	if !bytes.Equal(canonical, record.Request) || record.RequestID != req.RequestID || record.RequestDigest != digest {
		return errors.New("bundle request identity, bytes or digest mismatch")
	}
	intent, err := intentdoc.Parse(record.Intent)
	if err != nil {
		return err
	}
	canonical, _ = intent.Canonical()
	if !bytes.Equal(canonical, record.Intent) {
		return errors.New("bundle Intent is not canonical")
	}
	original, err := decodeIDs(record.OriginalIDs)
	if err != nil {
		return fmt.Errorf("bundle original IDs: %w", err)
	}
	base, err := decodeIDs(record.BaseIDs)
	if err != nil {
		return fmt.Errorf("bundle base IDs: %w", err)
	}
	baseRaw, err := ledgerBytes(base)
	if err != nil || !bytes.Equal(baseRaw, record.BaseIDs) {
		return errors.New("bundle base IDs are not canonical")
	}
	if base.Namespace != record.Namespace || (original.SchemaVersion == 3 && original.Namespace != base.Namespace) {
		return errors.New("bundle ID namespace mismatch")
	}
	reserved := map[string]bool{}
	for _, id := range base.Reserved {
		reserved[id] = true
	}
	for _, id := range original.Reserved {
		if !reserved[id] {
			return errors.New("bundle base dropped an original reservation")
		}
	}
	// External references were checked against real entries at admission. Their
	// identities suffice to reproduce immutable output without consulting files.
	seen := map[string]bool{}
	var entries []Entry
	for _, draft := range req.Tasks {
		for _, ref := range draft.DependsOn {
			if ref.TaskID == "" {
				continue
			}
			key := identityKey(ref.TaskID)
			if !reserved[key] {
				return errors.New("bundle external dependency is absent from base IDs")
			}
			if !seen[key] {
				entries = append(entries, Entry{Path: "todo/" + key + ".md", Card: card.View{ID: key, Status: "pending"}})
				seen[key] = true
			}
		}
	}
	expected, err := prepareTaskBundle(document, entries, base, intent)
	if err != nil {
		return fmt.Errorf("validate recorded bundle preparation: %w", err)
	}
	targetRaw, err := ledgerBytes(expected.Ledger)
	if err != nil || !bytes.Equal(targetRaw, record.TargetIDs) {
		return errors.New("bundle target IDs do not match its allocation")
	}
	if len(record.Cards) != len(expected.Cards) {
		return errors.New("bundle card count mismatch")
	}
	for i, item := range record.Cards {
		want := expected.Cards[i]
		if item.Key != req.Tasks[i].Key || item.ID != want.Entry.Card.ID || item.Path != want.Entry.Path || !bytes.Equal(item.Raw, want.Raw) {
			return fmt.Errorf("bundle card %d does not match prepared request", i)
		}
	}
	batchRaw, err := expected.Batch.Canonical()
	if err != nil || !bytes.Equal(batchRaw, record.Batch) {
		return errors.New("bundle Batch bytes do not match prepared request")
	}
	return nil
}
