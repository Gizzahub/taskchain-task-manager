package taskstore

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// policyActivationSnapshot hashes only board inputs that the activation
// transaction is expected to preserve. The caller supplies the already
// validated journal so an interrupted activation is not re-read through the
// ordinary policy/journal gate.
func policyActivationSnapshot(r *os.Root, policy boardpolicy.Policy, journal transitionJournal) (string, error) {
	return policyActivationSnapshotForAdoption(r, policy, journal, nil)
}

func policyActivationSnapshotForAdoption(r *os.Root, policy boardpolicy.Policy, journal transitionJournal, adoption *moduleIDAdoption) (string, error) {
	if err := validateTransitionRecords(journal, policy); err != nil {
		return "", err
	}
	for _, record := range journal.Records {
		if record.Kind == "pending" {
			return "", errors.New("pending transition must be recovered before policy activation")
		}
	}
	if err := checkBundleGate(r, journal); err != nil {
		return "", err
	}
	if err := checkStorageGate(r, journal); err != nil {
		return "", err
	}
	entries, err := listLockedWithPolicy(r, "", policy)
	if err != nil {
		return "", err
	}
	if err := validateGraph(entries); err != nil {
		return "", err
	}
	ledger, idsMode, err := moduleSnapshotLedger(r, adoption)
	if err != nil {
		return "", err
	}
	claims, err := loadClaims(r, entries)
	if err != nil {
		return "", err
	}
	for _, claim := range claims.Records {
		if claim.Status == "held" {
			return "", fmt.Errorf("held claim blocks policy activation: %s", claim.ID)
		}
	}
	if _, err := observedIDsWithRecords(entries, ledger, nil, claims, journal); err != nil {
		return "", err
	}

	h := sha256.New()
	// Preserve legacy snapshot bytes on boards without the new protocol.
	if journal.StorageProtocol >= 1 {
		raw, mode, err := snapshotActivationFile(r, repairsFile, maxRepairsBytes, true)
		if err != nil {
			return "", err
		}
		writeActivationFrame(h, "storage-receipts", raw)
		writeActivationFrame(h, "storage-mode", []byte(fmt.Sprintf("%o", mode)))
	}
	if journal.StorageProtocol >= 2 {
		raw, mode, err := snapshotActivationFile(r, relocationsFile, maxRepairsBytes, true)
		if err != nil {
			return "", err
		}
		writeActivationFrame(h, "relocation-receipts", raw)
		writeActivationFrame(h, "relocation-mode", []byte(fmt.Sprintf("%o", mode)))
	}
	if journal.StorageProtocol >= 3 {
		raw, mode, err := snapshotActivationFile(r, archivesFile, maxRepairsBytes, true)
		if err != nil {
			return "", err
		}
		writeActivationFrame(h, "archive-receipts", raw)
		writeActivationFrame(h, "archive-mode", []byte(fmt.Sprintf("%o", mode)))
	}
	// ID contents may be deliberately adopted by join. Their exact hashes are
	// part of the durable plan; permission changes remain snapshot conflicts.
	writeActivationFrame(h, "ids-mode", []byte(fmt.Sprintf("%o", idsMode)))
	for _, entry := range entries {
		raw, mode, err := snapshotActivationFile(r, entry.Path, maxCardBytes, true)
		if err != nil {
			return "", err
		}
		writeActivationFrame(h, "card", []byte(entry.Path))
		writeActivationFrame(h, "card-mode", []byte(fmt.Sprintf("%o", mode)))
		writeActivationFrame(h, "card-bytes", raw)
	}
	for _, item := range []struct {
		name     string
		limit    int
		required bool
	}{
		{name: claimsFile, limit: maxClaimsBytes},
		{name: bundlesFile, limit: maxBundleJournalBytes},
	} {
		raw, mode, err := snapshotActivationFile(r, item.name, item.limit, item.required)
		if err != nil {
			return "", err
		}
		writeActivationFrame(h, "file", []byte(item.name))
		writeActivationFrame(h, "file-exists", []byte{boolByte(raw != nil)})
		writeActivationFrame(h, "file-mode", []byte(fmt.Sprintf("%o", mode)))
		writeActivationFrame(h, "file-bytes", raw)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func snapshotActivationFile(r *os.Root, name string, limit int, required bool) ([]byte, fs.FileMode, error) {
	info, err := r.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		if required {
			return nil, 0, err
		}
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("snapshot file %s is not regular", name)
	}
	raw, err := boundedSnapshotFile(r, name, limit)
	if err != nil {
		return nil, 0, err
	}
	return raw, info.Mode(), nil
}

func writeActivationFrame(h interface{ Write([]byte) (int, error) }, label string, value []byte) {
	_, _ = h.Write([]byte(fmt.Sprintf("%d:%s:%d:", len(label), label, len(value))))
	_, _ = h.Write(value)
}

func boolByte(value bool) byte {
	if value {
		return '1'
	}
	return '0'
}
