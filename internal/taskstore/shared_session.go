package taskstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

type sharedSession struct {
	location   *githistory.BoardLocation
	root       *os.Root
	state      *sharedState
	commonInfo fs.FileInfo
}

// Every board operation takes this lock before a board lock, even before
// sharing is enabled. Thus claims, transitions and completed replay also
// participate in coordination with namespace activation.
func acquireShared(dir string, allowInitializing bool) (session *sharedSession, release func() error, err error) {
	return acquireSharedForBundle(dir, allowInitializing, false)
}

// The bundle writer alone may acquire a pending namespace; it must then
// match the request, board identity and actual owner path before any mutation.
func acquireSharedForBundle(dir string, allowInitializing, allowPendingBundle bool) (session *sharedSession, release func() error, err error) {
	return acquireSharedOptions(dir, allowInitializing, allowPendingBundle, false)
}

// Policy recovery may bypass only the policy pending gate, never a pending
// ID activation or task bundle. The recovery writer must match the saved plan.
func acquireSharedForPolicy(dir string) (session *sharedSession, release func() error, err error) {
	return acquireSharedOptions(dir, false, false, true)
}

func acquireSharedOptions(dir string, allowInitializing, allowPendingBundle, allowPendingPolicy bool) (session *sharedSession, release func() error, err error) {
	return acquireSharedStorageOptions(dir, allowInitializing, allowPendingBundle, allowPendingPolicy, false)
}

func acquireSharedStorageOptions(dir string, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair bool) (session *sharedSession, release func() error, err error) {
	return acquireSharedRelocationOptions(dir, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, false)
}

func acquireSharedRelocationOptions(dir string, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, allowPendingRelocation bool) (session *sharedSession, release func() error, err error) {
	return acquireSharedArchiveOptions(dir, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, allowPendingRelocation, false)
}

// Archive recovery may bypass only the archive pending gate. All existing
// callers reach this wrapper through acquireSharedRelocationOptions and keep
// the default false value.
func acquireSharedArchiveOptions(dir string, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, allowPendingRelocation, allowPendingArchive bool) (session *sharedSession, release func() error, err error) {
	return acquireSharedCapacityOptions(dir, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, allowPendingRelocation, allowPendingArchive, false)
}

func acquireSharedCapacityOptions(dir string, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, allowPendingRelocation, allowPendingArchive, allowPendingCapacity bool) (session *sharedSession, release func() error, err error) {
	return acquireSharedOwnerRejoinOptions(dir, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, allowPendingRelocation, allowPendingArchive, allowPendingCapacity, false)
}

// Owner-rejoin recovery is the only path allowed to observe its own common
// exclusion marker.  It still rejects every other pending operation.
func acquireSharedOwnerRejoin(dir string) (session *sharedSession, release func() error, err error) {
	return acquireSharedOwnerRejoinOptions(dir, true, false, false, false, false, false, false, true)
}

func acquireSharedOwnerRejoinOptions(dir string, allowInitializing, allowPendingBundle, allowPendingPolicy, allowPendingRepair, allowPendingRelocation, allowPendingArchive, allowPendingCapacity, allowPendingOwnerRejoin bool) (session *sharedSession, release func() error, err error) {
	location, err := githistory.LocateBoard(context.Background(), dir)
	if err != nil {
		return nil, nil, err
	}
	if location == nil {
		return nil, func() error { return nil }, nil
	}
	common, err := os.OpenRoot(location.CommonDirectory)
	if err != nil {
		return nil, nil, err
	}
	defer common.Close()
	commonInfo, err := common.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	name := "taskchain-task-manager"
	for _, part := range []string{"taskchain-task-manager", "ids", location.NamespaceKey} {
		if part == "taskchain-task-manager" {
			name = part
		} else {
			name = path.Join(name, part)
		}
		if err := common.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, nil, err
		}
		info, err := common.Lstat(name)
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errors.New("shared namespace path is not a real directory")
		}
	}
	r, err := common.OpenRoot(name)
	if err != nil {
		return nil, nil, err
	}
	unlock, err := lock(r)
	if err != nil {
		r.Close()
		return nil, nil, fmt.Errorf("shared ID namespace: %w", err)
	}
	release = func() error { return errors.Join(unlock(), r.Close()) }
	defer func() {
		if err != nil {
			err = errors.Join(err, release())
		}
	}()
	session = &sharedSession{location: location, root: r, commonInfo: commonInfo}
	state, err := loadSharedState(r)
	if errors.Is(err, fs.ErrNotExist) {
		return session, release, nil
	}
	if err != nil {
		return nil, release, err
	}
	if state.BoardPath != location.Board {
		return nil, release, errors.New("shared namespace board identity mismatch")
	}
	if state.Phase != "active" && !allowInitializing {
		return nil, release, errors.New("shared ID activation is initializing; use enable-shared --resume")
	}
	if state.PendingBundle != nil && !allowPendingBundle {
		return nil, release, errors.New("shared namespace has a pending bundle; recover from its original board")
	}
	if state.PendingRepair != nil && !allowPendingRepair {
		return nil, release, errors.New("shared namespace has a pending status repair; recover from its original board")
	}
	if state.PendingRelocation != nil && !allowPendingRelocation {
		return nil, release, errors.New("shared namespace has a pending relocation; recover from its original board")
	}
	if (state.PendingArchive != nil || state.PendingArchiveDelta != nil) && !allowPendingArchive {
		return nil, release, errors.New("shared namespace has a pending archive; recover from its original board")
	}
	if state.PendingArchiveCapacity != nil && !allowPendingCapacity {
		return nil, release, errors.New("shared namespace has a pending archive capacity adoption; recover from its original board")
	}
	if state.PendingOwnerRejoin != nil && !allowPendingOwnerRejoin {
		return nil, release, errors.New("shared namespace has a pending owner rejoin; recover from its original board")
	}
	if state.Policy != nil && state.Policy.Phase != "active" && !allowPendingPolicy {
		return nil, release, errors.New("shared policy activation is pending; explicit policy recovery required")
	}
	session.state = &state
	return session, release, nil
}

func (s *sharedSession) merge(ledger idLedger) (idLedger, error) {
	if err := s.verify(); err != nil {
		return ledger, err
	}
	if s == nil || s.state == nil {
		if ledger.SchemaVersion >= 3 {
			return ledger, errors.New("shared local binding has no common state; restore shared state, never reinitialize")
		}
		return ledger, nil
	}
	if ledger.SchemaVersion >= 3 && ledger.Namespace != s.state.NamespaceID {
		return ledger, errors.New("local shared namespace binding mismatch")
	}
	// Schema 4 is the owner-rejoin ledger: it carries the reservation floor a
	// returning board depends on, so joining shared work must never fold it
	// back down to schema 3.
	if ledger.SchemaVersion < 3 {
		ledger.SchemaVersion = 3
	}
	ledger.Namespace = s.state.NamespaceID
	ledger.Reserved = unionIDs(ledger.Reserved, s.state.Reserved)
	// The common state is the durable record of the reservation floor a rejoin
	// bootstrapped, and allocation reads the floor off the local ledger only.
	// Without folding it back a board whose ids.json lost its floors would
	// silently start reissuing IDs the floor exists to hold back, so the floor
	// has to survive the local file rather than depend on it.
	floors, err := unionReservationFloors(ledger.ReservationFloors, s.state.ReservationFloors)
	if err != nil {
		return ledger, err
	}
	if len(floors) > 0 {
		ledger.ReservationFloors, ledger.SchemaVersion = floors, 4
	}
	history, err := githistory.Scan(context.Background(), s.location.Repository, s.location.Board)
	if err != nil {
		return ledger, err
	}
	ledger.Reserved = unionIDs(ledger.Reserved, history.IDs)
	return ledger, nil
}

func (s *sharedSession) publish(ledger idLedger) error {
	if err := s.verify(); err != nil {
		return err
	}
	if _, err := ledgerBytes(ledger); err != nil {
		return err
	}
	if s == nil || s.state == nil {
		if ledger.SchemaVersion >= 3 {
			return errors.New("missing common state for shared ledger")
		}
		return nil
	}
	next := *s.state
	next.Reserved = unionIDs(next.Reserved, ledger.Reserved)
	// Publishing the floor is the other half of that: a floor established
	// locally is only durable once the common directory carries it too.
	floors, err := unionReservationFloors(next.ReservationFloors, ledger.ReservationFloors)
	if err != nil {
		return err
	}
	if len(floors) > 0 {
		next.ReservationFloors, next.SchemaVersion = floors, 4
	}
	if err := publishSharedState(s.root, next, false); err != nil {
		return err
	}
	s.state = &next
	return nil
}

func (s *sharedSession) verify() error {
	if s == nil {
		return nil
	}
	common, err := os.Lstat(s.location.CommonDirectory)
	if err != nil {
		return err
	}
	if !common.IsDir() || !os.SameFile(common, s.commonInfo) {
		return errors.New("Git common directory identity changed")
	}
	return verifyBoardHandle(s.root, filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey))
}

func (s *sharedSession) verifyBoard(r *os.Root) error {
	j, err := loadTransitions(r)
	if err != nil {
		return err
	}
	if err := s.verifyPolicyAuthority(r, j); err != nil {
		return err
	}
	if err := s.verifyBoardIdentity(r); err != nil {
		return err
	}
	return s.verifyLocalIDBinding(r)
}

// Identity validation is shared with pending-aware bundle sessions, whose
// policy validation must not recurse through the ordinary pending gate.
func (s *sharedSession) verifyBoardIdentity(r *os.Root) error {
	if s != nil {
		if err := s.verify(); err != nil {
			return err
		}
		if err := verifyBoardHandle(r, filepath.Join(s.location.Repository, filepath.FromSlash(s.location.Board))); err != nil {
			return err
		}
	}
	return nil
}

// Readers and receipt replay must not bypass an existing shared identity.
// This only checks identity; it never merges IDs, scans history or adopts a board.
func (s *sharedSession) verifyLocalIDBinding(r *os.Root) error {
	// Identity validation also runs while a policy activation is pending, so it
	// must not load transitions through policyForJournal and recurse into the
	// pending-policy gate. Capacity recovery resolves its exact original/target
	// state from the immutable payload rather than guessing from the protocol.
	archive, archiveErr := loadArchiveJournalForIdentityBinding(r)
	if archiveErr != nil && !errors.Is(archiveErr, fs.ErrNotExist) {
		return archiveErr
	}
	if archiveErr == nil {
		expectedNamespace := ""
		if s != nil && s.state != nil {
			expectedNamespace = s.state.NamespaceID
		}
		if archive.Namespace != expectedNamespace {
			return errors.New("local archive journal namespace mismatch")
		}
	}
	ledger, err := loadIDs(r)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if ledger.SchemaVersion != 3 && ledger.SchemaVersion != 4 {
		return nil
	}
	if s == nil || s.state == nil {
		return errors.New("shared local binding has no common state; restore shared state, never reinitialize")
	}
	if ledger.Namespace != s.state.NamespaceID {
		return errors.New("local shared namespace binding mismatch")
	}
	return nil
}

func unionIDs(groups ...[]string) []string {
	set := map[string]bool{}
	for _, group := range groups {
		for _, id := range group {
			set[id] = true
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
