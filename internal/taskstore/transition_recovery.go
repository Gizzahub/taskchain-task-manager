package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

func finishTransition(r *os.Root, j transitionJournal, rec transitionRecord, req TransitionRequest, step func(string) error) (TransitionResult, error) {
	if _, err := policyForJournal(r, j); err != nil {
		return TransitionResult{}, err
	}
	if cardpath.IsDocumentation(filepath.Base(rec.Source)) {
		return TransitionResult{}, errors.New("legacy transition uses a now-excluded documentation filename; finish recovery with the previous binary before upgrading; preserve the journal and cards")
	}
	if step != nil {
		if err := step("after-journal"); err != nil {
			return TransitionResult{}, err
		}
	}
	sourceExists, err := matchingTransitionFile(r, rec.Source, rec.Original, rec.Mode)
	if err != nil {
		return TransitionResult{}, err
	}
	targetExists, err := matchingTransitionFile(r, rec.Target, rec.Patched, rec.Mode)
	if err != nil {
		return TransitionResult{}, err
	}
	if !sourceExists && !targetExists {
		return TransitionResult{}, errors.New("source and target cards are both absent")
	}
	skip := ""
	if targetExists {
		skip = rec.Source
	}
	entries, err := listLockedExcept(r, skip)
	if err != nil {
		return TransitionResult{}, err
	}
	claims, err := loadClaims(r, entries)
	if err != nil {
		return TransitionResult{}, err
	}
	held := false
	for _, claim := range claims.Records {
		if sameIdentity(claim.ID, req.ID) && claim.Owner == req.Owner && claim.Token == req.Token && claim.Status == outputvocab.ClaimHeld {
			held = true
		}
	}
	if !held {
		return TransitionResult{}, errors.New("matching held claim not found")
	}
	if !targetExists {
		if err := relocationParents(r, rec.Target, true); err != nil {
			return TransitionResult{}, err
		}
		name, err := stageWith(r, rec.Patched, func(f *os.File, raw []byte) error {
			n, err := f.Write(raw)
			if err != nil {
				return err
			}
			if n != len(raw) {
				return io.ErrShortWrite
			}
			if err := f.Chmod(os.FileMode(rec.Mode)); err != nil {
				return err
			}
			return f.Sync()
		})
		if err != nil {
			return TransitionResult{}, err
		}
		if err := r.Link(name, rec.Target); err != nil {
			return TransitionResult{}, errors.Join(err, r.Remove(name))
		}
		if err := r.Remove(name); err != nil {
			return TransitionResult{}, err
		}
		if err := syncRelocationDirectory(r, filepath.Dir(rec.Target)); err != nil {
			return TransitionResult{}, err
		}
		if step != nil {
			if err := step("after-target"); err != nil {
				return TransitionResult{}, err
			}
		}
	}
	// Recheck both files immediately before deleting the original. External
	// editors do not honor the lock; changed bytes must never be discarded.
	if _, err := policyForJournal(r, j); err != nil {
		return TransitionResult{}, err
	}
	targetExists, err = matchingTransitionFile(r, rec.Target, rec.Patched, rec.Mode)
	if err != nil {
		return TransitionResult{}, err
	}
	if !targetExists {
		return TransitionResult{}, errors.New("published transition target disappeared")
	}
	sourceExists, err = matchingTransitionFile(r, rec.Source, rec.Original, rec.Mode)
	if err != nil {
		return TransitionResult{}, err
	}
	if sourceExists {
		if err := r.Remove(rec.Source); err != nil {
			return TransitionResult{}, err
		}
		if err := syncRelocationDirectory(r, filepath.Dir(rec.Source)); err != nil {
			return TransitionResult{}, err
		}
	}
	if step != nil {
		if err := step("after-source"); err != nil {
			return TransitionResult{}, err
		}
	}
	for i := range j.Records {
		if j.Records[i].RequestID == req.RequestID {
			j.Records[i].Kind = "completed"
			j.Records[i].Status = "completed"
			j.Records[i].Original = nil
			j.Records[i].Patched = nil
		}
	}
	if err := publishTransitionJournal(r, j); err != nil {
		return TransitionResult{}, err
	}
	if step != nil {
		if err := step("after-receipt"); err != nil {
			return TransitionResult{}, err
		}
	}
	return transitionResult(rec), nil
}

func matchingTransitionFile(r *os.Root, path string, want []byte, mode uint32) (bool, error) {
	if err := relocationParents(r, path, false); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	parent, err := r.Lstat(filepath.Dir(path))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if parent.Mode()&os.ModeSymlink != 0 || !parent.IsDir() {
		return false, fmt.Errorf("transition parent is not a real directory: %s", path)
	}
	info, err := r.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("transition card is not regular: %s", path)
	}
	if uint32(info.Mode().Perm()) != mode {
		return false, fmt.Errorf("transition card mode changed: %s", path)
	}
	raw, err := readTransitionCard(r, path)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(raw, want) {
		return false, fmt.Errorf("transition card bytes changed: %s", path)
	}
	return true, nil
}

func readTransitionCard(r *os.Root, path string) ([]byte, error) {
	f, err := r.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxCardBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxCardBytes {
		return nil, errors.New("transition card exceeds 1 MiB")
	}
	return raw, nil
}

func ensureTransitionDir(r *os.Root, dir string) error {
	info, err := r.Lstat(dir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("transition target directory is not real: %s", dir)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return r.Mkdir(dir, 0o755)
}
