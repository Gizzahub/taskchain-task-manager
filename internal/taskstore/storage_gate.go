package taskstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func canonicalStorageBoard(r *os.Root) (string, error) {
	name, err := filepath.Abs(r.Name())
	if err != nil {
		return "", err
	}
	name, err = filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	if err := verifyBoardHandle(r, name); err != nil {
		return "", err
	}
	return name, nil
}

// This gate is also used by exceptional bundle/policy sessions. A storage
// marker is persistent even after every repair has completed.
func checkStorageGate(r *os.Root, transitions transitionJournal) error {
	j, err := loadRepairJournal(r)
	if errors.Is(err, fs.ErrNotExist) {
		if transitions.StorageProtocol == 0 {
			return nil
		}
		return errors.New("storage journal missing from adopted board; restore it, never reinitialize")
	}
	if err != nil {
		return err
	}
	if transitions.StorageProtocol != 1 {
		return errors.New("storage adoption is incomplete; resume explicit adoption")
	}
	board, err := canonicalStorageBoard(r)
	if err != nil {
		return err
	}
	if j.BoardPath != board {
		return errors.New("storage journal belongs to a different board")
	}
	for _, record := range j.Records {
		if record.Kind == "pending" {
			return errors.New("pending status repair requires exact request recovery")
		}
	}
	if _, err := loadIDs(r); err != nil {
		return fmt.Errorf("storage-adopted board requires its ID ledger: %w", err)
	}
	return nil
}

func storageStep(step func(string) error, point string) error {
	if step != nil {
		return step(point)
	}
	return nil
}
