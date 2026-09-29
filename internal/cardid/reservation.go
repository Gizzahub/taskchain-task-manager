package cardid

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MaxCardNumber is the largest number a card filename can spell. A filename
// holds at most three digits, so 1000 is not a card number; reservation
// refuses anything higher instead of widening the pattern.
const MaxCardNumber = 999

// ErrNumberSpaceFull is the refusal for a number past MaxCardNumber. The
// text is the contract: the dialect number space is full. The caller did not
// submit an invalid id; the system ran out of numbers it can spell.
var ErrNumberSpaceFull = errors.New("the dialect number space is full")

// The ledger layout under the runtime-state root ({git common dir}/ce):
// card-id-reservations/highest.json holds, per id prefix, the highest number
// ever reserved. The lock file exists only inside a reservation.
const (
	reservationDirName  = "card-id-reservations"
	reservationFileName = "highest.json"
	reservationLockName = "mutation.lock"
)

// ReservationLedger is the cross-worktree card-ID reservation ledger. A tree
// scan sees only its own worktree, so two worktrees creating a card at the
// same moment compute the same "highest" and both land on one number; the
// ledger lives in the git common dir, which every worktree of the repository
// shares, and remembers each number whether or not a card followed it.
type ReservationLedger struct {
	baseDir string
}

// NewReservationLedger wires the ledger to a runtime-state root, the
// {git common dir}/ce directory a caller resolves.
func NewReservationLedger(baseDir string) *ReservationLedger {
	return &ReservationLedger{baseDir: baseDir}
}

// Next reserves and returns the smallest integer strictly greater than floor
// that the ledger has not already reserved for prefix. The read, the
// compute, and the write happen inside one O_EXCL sentinel lock, so callers
// in different processes never receive the same value: a caller that read a
// "highest so far" and wrote it back in two separate steps would still race
// a second worktree between the two.
//
// When the next number would pass MaxCardNumber the ledger refuses without
// writing: reserving 1000 and then failing the filename pattern would burn
// the number, and the next try would be 1001. The refusal is that the
// number space is full, not that the caller passed an invalid id.
func (l *ReservationLedger) Next(prefix string, floor int) (int, error) {
	dir := filepath.Join(l.baseDir, reservationDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("create card id reservation dir: %w", err)
	}
	unlock, err := lockReservation(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()

	ledger, err := readReservationLedger(filepath.Join(dir, reservationFileName))
	if err != nil {
		return 0, err
	}
	highest := ledger[prefix]
	if floor > highest {
		highest = floor
	}
	next := highest + 1
	if next > MaxCardNumber {
		return 0, ErrNumberSpaceFull
	}
	ledger[prefix] = next
	if err := writeReservationLedger(dir, ledger); err != nil {
		return 0, err
	}
	return next, nil
}

func readReservationLedger(path string) (map[string]int, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read card id reservation ledger: %w", err)
	}
	ledger := map[string]int{}
	if err := json.Unmarshal(b, &ledger); err != nil {
		return nil, fmt.Errorf("decode card id reservation ledger: %w", err)
	}
	return ledger, nil
}

// writeReservationLedger replaces the ledger through a temporary file and a
// rename, syncing both file and directory: a reservation that a crash can
// quietly forget hands its number out a second time.
func writeReservationLedger(dir string, ledger map[string]int) error {
	b, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return fmt.Errorf("encode card id reservation ledger: %w", err)
	}
	b = append(b, '\n')
	path := filepath.Join(dir, reservationFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write card id reservation ledger: %w", err)
	}
	if err := syncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace card id reservation ledger: %w", err)
	}
	return syncDir(dir)
}

const (
	reservationLockPoll       = 2 * time.Millisecond
	reservationLockStaleAfter = 5 * time.Second
)

// lockReservation acquires the sentinel and returns the func that releases
// it. A holder that keeps replacing the sentinel is a queue making progress;
// one that has sat unreplaced past reservationLockStaleAfter is presumed
// dead rather than slow, and is reported with its path so an operator can
// inspect and remove it.
func lockReservation(dir string) (func(), error) {
	path := filepath.Join(dir, reservationLockName)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return claimReservationLock(f, path)
		}
		if !os.IsExist(err) {
			return nil, err
		}
		info, statErr := os.Stat(path)
		if os.IsNotExist(statErr) {
			// Released between the failed create and the stat: progress,
			// not staleness. Contend again immediately.
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("stat card id reservation lock: %w", statErr)
		}
		if time.Since(info.ModTime()) > reservationLockStaleAfter {
			return nil, fmt.Errorf("card id reservation lock has sat unreplaced for over %s and is presumed abandoned rather than contended; inspect and remove it: %s", reservationLockStaleAfter, path)
		}
		time.Sleep(reservationLockPoll)
	}
}

func claimReservationLock(f *os.File, path string) (func(), error) {
	if _, err := fmt.Fprintf(f, "pid=%d created=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write card id reservation lock metadata: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close card id reservation lock: %w", err)
	}
	return func() { _ = os.Remove(path) }, nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
