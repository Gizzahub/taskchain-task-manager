package taskstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

type sharedPolicyBoard struct {
	root   *os.Root
	unlock func() error
	path   string
	head   string
}

func closeSharedPolicyBoards(boards []sharedPolicyBoard) error {
	var err error
	for i := len(boards) - 1; i >= 0; i-- {
		err = errors.Join(err, boards[i].unlock(), boards[i].root.Close())
	}
	return err
}

func openSharedPolicyBoards(s *sharedSession, phase string, plans []policyActivationPlan) (boards []sharedPolicyBoard, err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, closeSharedPolicyBoards(boards))
			boards = nil
		}
	}()
	inventory, err := githistory.InspectWorktrees(context.Background(), s.location.Repository, s.location.Board)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	if len(plans) > 0 {
		for _, p := range plans {
			wanted[p.Root] = true
		}
	} else if phase == "joining" {
		wanted[filepath.Join(s.location.Repository, filepath.FromSlash(s.location.Board))] = true
	}
	if (phase == "initializing" || phase == "revising") && len(plans) > 0 && len(inventory.Worktrees) != len(plans) {
		return nil, errors.New("policy activation worktree inventory changed")
	}
	targets := []githistory.Worktree{}
	for _, wt := range inventory.Worktrees {
		path := filepath.Join(wt.Path, filepath.FromSlash(s.location.Board))
		if len(wanted) > 0 && !wanted[path] {
			continue
		}
		if wt.Bare || wt.Locked || wt.Prunable {
			return nil, fmt.Errorf("policy activation requires accessible unlocked worktree: %s", wt.Path)
		}
		targets = append(targets, wt)
	}
	if len(targets) == 0 || (len(wanted) > 0 && len(targets) != len(wanted)) {
		return nil, errors.New("policy activation owner is absent from worktree inventory")
	}
	sort.Slice(targets, func(i, j int) bool {
		return filepath.Join(targets[i].Path, s.location.Board) < filepath.Join(targets[j].Path, s.location.Board)
	})
	for _, wt := range targets {
		observed, err := githistory.InspectWorktrees(context.Background(), wt.Path, s.location.Board)
		if err != nil {
			return boards, err
		}
		if observed.CommonDirectory != inventory.CommonDirectory || observed.NamespaceKey != inventory.NamespaceKey {
			return boards, errors.New("policy board belongs to another Git namespace")
		}
		path := filepath.Join(wt.Path, filepath.FromSlash(s.location.Board))
		r, err := openBoard(path)
		if err != nil {
			return boards, err
		}
		unlock, err := lock(r)
		if err != nil {
			return boards, errors.Join(err, r.Close())
		}
		boards = append(boards, sharedPolicyBoard{root: r, unlock: unlock, path: path, head: wt.HEAD})
		if err := verifyBoardHandle(r, path); err != nil {
			return boards, err
		}
	}
	if len(plans) > 0 {
		for i, p := range plans {
			if p.Root != boards[i].path || p.HEAD != boards[i].head {
				return boards, errors.New("policy activation worktree HEAD or owner changed")
			}
		}
	}
	return boards, nil
}

func verifySharedPolicyInventory(s *sharedSession, boards []sharedPolicyBoard, phase string) error {
	if err := verifyPolicyCommonState(s); err != nil {
		return err
	}
	inventory, err := githistory.InspectWorktrees(context.Background(), s.location.Repository, s.location.Board)
	if err != nil {
		return err
	}
	if (phase == "initializing" || phase == "revising") && len(inventory.Worktrees) != len(boards) {
		return errors.New("policy activation worktree inventory changed")
	}
	for _, b := range boards {
		found := false
		for _, wt := range inventory.Worktrees {
			if filepath.Join(wt.Path, filepath.FromSlash(s.location.Board)) == b.path {
				found = wt.HEAD == b.head && !wt.Bare && !wt.Locked && !wt.Prunable
				break
			}
		}
		if !found {
			return errors.New("policy activation worktree HEAD or owner changed")
		}
		if err := verifyBoardHandle(b.root, b.path); err != nil {
			return err
		}
	}
	return nil
}

func verifyPolicyCommonState(s *sharedSession) error {
	if err := s.verify(); err != nil {
		return err
	}
	current, err := loadSharedState(s.root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, *s.state) {
		return errors.New("shared policy authority changed while locked; preserve transaction")
	}
	return nil
}
