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

// Every ID writer takes this lock before a board lock, even before sharing is
// enabled. Thus a local-mode decision cannot race with namespace activation.
func acquireShared(dir string, allowInitializing bool) (session *sharedSession, release func() error, err error) {
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
	session.state = &state
	return session, release, nil
}

func (s *sharedSession) merge(ledger idLedger) (idLedger, error) {
	if err := s.verify(); err != nil {
		return ledger, err
	}
	if s == nil || s.state == nil {
		if ledger.SchemaVersion == 3 {
			return ledger, errors.New("shared local binding has no common state; restore shared state, never reinitialize")
		}
		return ledger, nil
	}
	if ledger.SchemaVersion == 3 && ledger.Namespace != s.state.NamespaceID {
		return ledger, errors.New("local shared namespace binding mismatch")
	}
	ledger.SchemaVersion = 3
	ledger.Namespace = s.state.NamespaceID
	ledger.Reserved = unionIDs(ledger.Reserved, s.state.Reserved)
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
		if ledger.SchemaVersion == 3 {
			return errors.New("missing common state for shared ledger")
		}
		return nil
	}
	next := *s.state
	next.Reserved = unionIDs(next.Reserved, ledger.Reserved)
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
	if s == nil {
		return nil
	}
	if err := s.verify(); err != nil {
		return err
	}
	return verifyBoardHandle(r, filepath.Join(s.location.Repository, filepath.FromSlash(s.location.Board)))
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
