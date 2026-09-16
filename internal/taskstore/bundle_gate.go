package taskstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

func checkBundleGate(r *os.Root, j transitionJournal) error {
	bundles, err := loadBundles(r)
	if errors.Is(err, fs.ErrNotExist) {
		if j.BundleProtocol == 0 {
			return nil
		}
		return errors.New("bundle journal missing from adopted board; restore it, never reinitialize")
	}
	if err != nil {
		return err
	}
	if j.BundleProtocol != 1 {
		return errors.New("bundle adoption is incomplete; use explicit bundle adoption recovery")
	}
	for _, record := range bundles.Records {
		if record.Status == "pending" {
			return errors.New("pending bundle requires exact request recovery")
		}
	}
	// Adoption cannot reconstruct a fresh ID ledger from today's cards: a
	// completed bundle may refer to cards that have since been removed.
	if _, err := loadIDs(r); err != nil {
		return fmt.Errorf("bundle-adopted board requires its ID ledger; restore it, never reinitialize: %w", err)
	}
	return nil
}
