package taskstore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

type SharedResult struct {
	NamespaceID   string `json:"namespaceId"`
	Phase         string `json:"phase"`
	Worktrees     int    `json:"worktrees"`
	ReservedCount int    `json:"reservedCount"`
}

func EnableShared(dir string, resume bool) (SharedResult, error) {
	return enableSharedStep(dir, resume, nil)
}

func enableSharedStep(dir string, resume bool, step func(string) error) (result SharedResult, err error) {
	s, release, err := acquireShared(dir, true)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, release()) }()
	if s == nil {
		return result, errors.New("shared IDs require a Git worktree board")
	}
	if s.state != nil && s.state.Phase == "initializing" && !resume {
		return result, errors.New("activation interrupted; explicit --resume required")
	}
	if s.state == nil && resume {
		return result, errors.New("no shared activation exists to resume")
	}
	inventory, err := githistory.InspectWorktrees(context.Background(), s.location.Repository, s.location.Board)
	if err != nil {
		return result, err
	}
	boards := []activationBoard{}
	defer func() {
		for i := len(boards) - 1; i >= 0; i-- {
			err = errors.Join(err, boards[i].root.Remove(".task-manager.lock"), boards[i].root.Close())
		}
	}()
	for _, wt := range inventory.Worktrees {
		if wt.Bare || wt.Prunable || wt.Locked {
			return result, fmt.Errorf("activation requires accessible unlocked worktree: %s", wt.Path)
		}
		boardDir := filepath.Join(wt.Path, filepath.FromSlash(s.location.Board))
		observed, err := githistory.InspectWorktrees(context.Background(), wt.Path, s.location.Board)
		if err != nil {
			return result, err
		}
		if observed.CommonDirectory != inventory.CommonDirectory || observed.NamespaceKey != inventory.NamespaceKey {
			return result, errors.New("worktree board belongs to another namespace")
		}
		r, err := openBoard(boardDir)
		if err != nil {
			return result, err
		}
		if _, err := lock(r); err != nil {
			r.Close()
			return result, err
		}
		boards = append(boards, activationBoard{root: r, participant: sharedParticipant{Root: wt.Path, HEAD: wt.HEAD}})
		journal, err := loadTransitions(r)
		if err != nil {
			return result, err
		}
		if err := s.verifyPolicyAuthority(r, journal); err != nil {
			return result, err
		}
		b := &boards[len(boards)-1]
		b.participant.Snapshot, b.ledger, b.participant.OriginalLedger, err = activationSnapshot(r)
		if err != nil {
			return result, err
		}
	}
	if s.state != nil && s.state.Phase == "active" {
		for _, b := range boards {
			if b.ledger.SchemaVersion != 3 || b.ledger.Namespace != s.state.NamespaceID {
				return result, errors.New("active namespace has an unbound worktree; explicitly initialize or adopt its local ledger")
			}
		}
		return sharedResult(*s.state), nil
	}
	var state sharedState
	if s.state == nil {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return result, err
		}
		state = sharedState{SchemaVersion: 1, NamespaceID: fmt.Sprintf("%x", token), BoardPath: s.location.Board, Phase: "initializing", Reserved: []string{}, Participants: []sharedParticipant{}}
		for _, b := range boards {
			if b.ledger.SchemaVersion == 3 {
				return result, errors.New("orphaned shared local binding; restore common state")
			}
			state.Reserved = unionIDs(state.Reserved, b.ledger.Reserved)
		}
		history, err := githistory.Scan(context.Background(), s.location.Repository, s.location.Board)
		if err != nil {
			return result, err
		}
		state.Reserved = unionIDs(state.Reserved, history.IDs)
		target, err := ledgerBytes(idLedger{SchemaVersion: 3, Reserved: state.Reserved, Namespace: state.NamespaceID})
		if err != nil {
			return result, err
		}
		for _, b := range boards {
			p := b.participant
			p.TargetLedger = bytesDigest(target)
			state.Participants = append(state.Participants, p)
		}
		if err := s.verify(); err != nil {
			return result, err
		}
		if err := publishSharedState(s.root, state, true); err != nil {
			return result, err
		}
	} else {
		state = *s.state
	}
	if step != nil {
		if err := step("after-initializing"); err != nil {
			return result, err
		}
	}
	if err := verifyActivationBoards(boards, state); err != nil {
		return result, err
	}
	if err := verifyActivationHandles(boards, state.BoardPath); err != nil {
		return result, err
	}
	target := idLedger{SchemaVersion: 3, Reserved: state.Reserved, Namespace: state.NamespaceID}
	for i, b := range boards {
		if err := verifyBoardHandle(b.root, filepath.Join(b.participant.Root, filepath.FromSlash(state.BoardPath))); err != nil {
			return result, err
		}
		if err := publishIDs(b.root, target, false); err != nil {
			return result, err
		}
		if step != nil {
			if err := step(fmt.Sprintf("after-local-%d", i)); err != nil {
				return result, err
			}
		}
	}
	after, err := githistory.InspectWorktrees(context.Background(), s.location.Repository, s.location.Board)
	if err != nil {
		return result, err
	}
	if len(after.Worktrees) != len(state.Participants) {
		return result, errors.New("activation worktree inventory changed")
	}
	for i, wt := range after.Worktrees {
		p := state.Participants[i]
		if wt.Path != p.Root || wt.HEAD != p.HEAD || wt.Prunable || wt.Locked || wt.Bare {
			return result, errors.New("activation worktree inventory changed")
		}
	}
	// Re-read under all locks, catching non-cooperating edits before activation.
	for i := range boards {
		boards[i].participant.Snapshot, boards[i].ledger, boards[i].participant.OriginalLedger, err = activationSnapshot(boards[i].root)
		if err != nil {
			return result, err
		}
	}
	if err := verifyActivationBoards(boards, state); err != nil {
		return result, err
	}
	state.Phase = "active"
	if err := verifyActivationHandles(boards, state.BoardPath); err != nil {
		return result, err
	}
	if err := s.verify(); err != nil {
		return result, err
	}
	if err := publishSharedState(s.root, state, false); err != nil {
		return result, err
	}
	if step != nil {
		if err := step("after-active"); err != nil {
			return result, err
		}
	}
	return sharedResult(state), nil
}

func verifyActivationBoards(boards []activationBoard, state sharedState) error {
	raw, err := ledgerBytes(idLedger{SchemaVersion: 3, Reserved: state.Reserved, Namespace: state.NamespaceID})
	if err != nil {
		return err
	}
	targetHash := bytesDigest(raw)
	reserved := map[string]bool{}
	for _, id := range state.Reserved {
		reserved[id] = true
	}
	if len(boards) != len(state.Participants) {
		return errors.New("activation participant count changed")
	}
	for i, b := range boards {
		p := state.Participants[i]
		if p.TargetLedger != targetHash {
			return errors.New("activation target hash does not match shared reservations")
		}
		for _, id := range b.ledger.Reserved {
			if !reserved[id] {
				return errors.New("activation shared reservations omit observed local identity")
			}
		}
		if p.Root != b.participant.Root || p.HEAD != b.participant.HEAD || p.Snapshot != b.participant.Snapshot {
			return errors.New("activation board snapshot changed")
		}
		if b.participant.OriginalLedger != p.OriginalLedger && b.participant.OriginalLedger != p.TargetLedger {
			return errors.New("activation local ledger changed")
		}
		if b.ledger.SchemaVersion == 3 && b.ledger.Namespace != state.NamespaceID {
			return errors.New("activation local namespace mismatch")
		}
	}
	return nil
}

func sharedResult(s sharedState) SharedResult {
	return SharedResult{NamespaceID: s.NamespaceID, Phase: s.Phase, Worktrees: len(s.Participants), ReservedCount: len(s.Reserved)}
}
