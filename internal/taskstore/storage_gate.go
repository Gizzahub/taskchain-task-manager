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
	if err := checkRelocationGate(r, transitions); err != nil {
		return err
	}
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
	if transitions.StorageProtocol != 1 && transitions.StorageProtocol != 2 {
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

func checkRelocationGate(r *os.Root, transitions transitionJournal) error {
	j, err := loadRelocationJournal(r)
	if errors.Is(err, fs.ErrNotExist) {
		if transitions.StorageProtocol < 2 {
			return nil
		}
		return errors.New("relocation journal missing from adopted board; restore it")
	}
	if err != nil {
		return err
	}
	if transitions.StorageProtocol != 2 {
		return errors.New("relocation adoption is incomplete; resume explicit adoption")
	}
	board, err := canonicalStorageBoard(r)
	if err != nil {
		return err
	}
	if board != j.BoardPath {
		return errors.New("relocation journal belongs to another board")
	}
	for _, rec := range j.Records {
		if rec.Kind == "pending" {
			return errors.New("pending relocation requires exact request recovery")
		}
	}
	return nil
}

func storageStep(step func(string) error, point string) error {
	if step != nil {
		return step(point)
	}
	return nil
}
