package taskstore

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type activationBoard struct {
	root        *os.Root
	participant sharedParticipant
	ledger      idLedger
}

func verifyBoardHandle(r *os.Root, dir string) error {
	opened, err := r.Stat(".")
	if err != nil {
		return err
	}
	current, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	for parent := dir; ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("bound directory path contains symlink or non-directory")
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	if !os.SameFile(opened, current) {
		return errors.New("board directory identity changed; preserve activation state")
	}
	return nil
}

func verifyActivationHandles(boards []activationBoard, boardPath string) error {
	for _, b := range boards {
		if err := verifyBoardHandle(b.root, filepath.Join(b.participant.Root, filepath.FromSlash(boardPath))); err != nil {
			return err
		}
	}
	return nil
}

func bytesDigest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func ledgerBytes(ledger idLedger) ([]byte, error) {
	raw, err := json.Marshal(ledger)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > maxIDsBytes {
		return nil, errors.New("local ID ledger exceeds 1 MiB")
	}
	return raw, nil
}

// Snapshot only task-owned semantic inputs, with framing to avoid ambiguous
// concatenation. All board locks are held by the caller through activation.
func activationSnapshot(r *os.Root) (string, idLedger, string, error) {
	return activationSnapshotWithArchive(r, true)
}

func activationSnapshotWithArchive(r *os.Root, includeArchive bool) (string, idLedger, string, error) {
	if err := rejectPendingTransitions(r); err != nil {
		return "", idLedger{}, "", err
	}
	entries, err := listLocked(r)
	if err != nil {
		return "", idLedger{}, "", err
	}
	if err := validateGraph(entries); err != nil {
		return "", idLedger{}, "", err
	}
	ledger, err := loadIDs(r)
	if err != nil {
		return "", ledger, "", err
	}
	observed, err := observedIDs(r, entries, ledger, nil)
	if err != nil {
		return "", ledger, "", err
	}
	h := sha256.New()
	for _, entry := range entries {
		raw, err := readTransitionCard(r, entry.Path)
		if err != nil {
			return "", ledger, "", err
		}
		info, err := r.Lstat(entry.Path)
		if err != nil {
			return "", ledger, "", err
		}
		fmt.Fprintf(h, "%d:%s:%o:%d:", len(entry.Path), entry.Path, info.Mode(), len(raw))
		h.Write(raw)
	}
	names := []string{claimsFile, transitionsFile, policyFile}
	journal, err := loadTransitions(r)
	if err != nil {
		return "", ledger, "", err
	}
	if journal.StorageProtocol >= 1 {
		names = append(names, repairsFile)
	}
	if journal.StorageProtocol >= 2 {
		names = append(names, relocationsFile)
	}
	if journal.StorageProtocol >= 3 && includeArchive {
		raw, mode, err := snapshotActivationFile(r, archivesFile, maxRepairsBytes, true)
		if err != nil {
			return "", ledger, "", err
		}
		writeActivationFrame(h, "archive-receipts", raw)
		writeActivationFrame(h, "archive-mode", []byte(fmt.Sprintf("%o", mode)))
	}
	for _, name := range names {
		raw, err := boundedSnapshotFile(r, name, maxTransitionBytes)
		if errors.Is(err, fs.ErrNotExist) {
			if name == policyFile {
				// Keep pre-policy initializing snapshots resumable after upgrade.
				continue
			}
			fmt.Fprintf(h, "missing:%s;", name)
			continue
		}
		if err != nil {
			return "", ledger, "", err
		}
		fmt.Fprintf(h, "%d:%s:%d:", len(name), name, len(raw))
		h.Write(raw)
	}
	raw, err := boundedSnapshotFile(r, idsFile, maxIDsBytes)
	if err != nil {
		return "", ledger, "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), observed, bytesDigest(raw), nil
}

func boundedSnapshotFile(r *os.Root, name string, limit int) ([]byte, error) {
	info, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("invalid snapshot file type or size")
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errors.New("snapshot exceeds size limit")
	}
	return raw, nil
}
