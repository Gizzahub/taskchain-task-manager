package taskflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxCardNumber is the three-digit card number space the filename pattern
// (%03d-slug.md) can hold.
const MaxCardNumber = 999

// Reservation paths inside the git common dir. This ledger is the CE-format
// store that de-duplicates `task new` across worktrees; it is separate from
// the product's own .task-manager-ids.json system, which claims IDs through
// the taskstore protocol instead.
const (
	reservationDir  = "card-id-reservations"
	reservationFile = "highest.json"
	reservationLock = "mutation.lock"
)

// ReservationStore allocates card numbers under the git common dir.
type ReservationStore struct {
	dir string
}

// NewReservationStore aims the store at <gitCommonDir>/ce.
func NewReservationStore(gitCommonDir string) *ReservationStore {
	return &ReservationStore{dir: filepath.Join(gitCommonDir, reservationDir)}
}

func (s *ReservationStore) ledgerPath() string { return filepath.Join(s.dir, reservationFile) }
func (s *ReservationStore) lockPath() string   { return filepath.Join(s.dir, reservationLock) }

// Next returns the highest+1 number for prefix. floor is what the working
// tree and refs already show; the ledger remembers numbers whether or not a
// card followed them, so a refused create never reissues one.
func (s *ReservationStore) Next(prefix string, floor int) (int, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return 0, fmt.Errorf("create card id reservation dir: %w", err)
	}
	unlock, err := s.lock()
	if err != nil {
		return 0, err
	}
	defer unlock()

	ledger, err := s.load()
	if err != nil {
		return 0, err
	}
	highest := ledger[prefix]
	if floor > highest {
		highest = floor
	}
	next := highest + 1
	if next > MaxCardNumber {
		return 0, fmt.Errorf("card number space for %s is full (%d)", prefix, MaxCardNumber)
	}
	ledger[prefix] = next
	if err := s.save(ledger); err != nil {
		return 0, err
	}
	return next, nil
}

func (s *ReservationStore) lock() (func(), error) {
	deadline := time.Now().Add(time.Second)
	for {
		f, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(s.lockPath()) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create card id reservation lock: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("card id reservation lock is held: %s", s.lockPath())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *ReservationStore) load() (map[string]int, error) {
	b, err := os.ReadFile(s.ledgerPath())
	if os.IsNotExist(err) {
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

func (s *ReservationStore) save(ledger map[string]int) error {
	b, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return fmt.Errorf("encode card id reservation ledger: %w", err)
	}
	tmp := filepath.Join(s.dir, fmt.Sprintf("%s.%d.tmp", reservationFile, time.Now().UnixNano()))
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write card id reservation ledger: %w", err)
	}
	if err := os.Rename(tmp, s.ledgerPath()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish card id reservation ledger: %w", err)
	}
	return nil
}

// TreeFloor scans the tasks tree — archive included, so an archived card's
// number is never handed out twice — for the highest frontmatter id of prefix.
// Like the pinned allocator, the floor reads card ids only: a filename is free
// and a frontmatter id is the card's own claim, and the two can disagree.
func TreeFloor(root, prefix string) int {
	highest := 0
	_ = filepath.WalkDir(filepath.Join(root, TasksDir), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		if card, err := ReadCard(root, relToTasks(root, p)); err == nil && card != nil {
			if id, ok := parseCardNumber(prefix, card.ID); ok && id > highest {
				highest = id
			}
		}
		return nil
	})
	return highest
}

func relToTasks(root, p string) string {
	rel, err := filepath.Rel(filepath.Join(root, TasksDir), p)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func parseCardNumber(prefix, id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, prefix+"-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

// RefFloor scans git history for the highest number cards of prefix carry on
// any ref. Like the pinned scanner, a failure is returned, never folded into
// zero: a floor that silently reports empty reissues numbers.
func RefFloor(ctx context.Context, root, prefix string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotepath=false", "--no-replace-objects",
		"--no-lazy-fetch", "log", "--all", "--format=%s %b", "--", TasksDir)
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return 0, fmt.Errorf("git log --all: %w: %s", err, msg)
		}
		return 0, fmt.Errorf("git log --all: %w", err)
	}
	highest := 0
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + `-(\d+)`)
	for _, m := range re.FindAllStringSubmatch(string(out), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
			highest = n
		}
	}
	return highest, nil
}

// GitCommonDir resolves the git common directory of root, reporting whether
// one exists. Without a repository there is no shared ledger and the creator
// falls back to its own tree scan.
func GitCommonDir(root string) (string, bool) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir), true
}
