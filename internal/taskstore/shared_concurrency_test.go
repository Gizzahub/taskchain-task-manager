package taskstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSharedCreateHelper(t *testing.T) {
	if os.Getenv("TASKSTORE_SHARED_HELPER") != "1" {
		return
	}
	board := os.Args[len(os.Args)-2]
	id := os.Args[len(os.Args)-1]
	for attempt := 0; attempt < 100; attempt++ {
		req := CreateRequest{Title: "subprocess"}
		if id != "-" {
			req.ID = id
		}
		entry, err := Create(board, req)
		if err == nil {
			fmt.Print(entry.Card.ID)
			os.Exit(0)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "locked") {
			fmt.Fprint(os.Stderr, err)
			os.Exit(2)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Fprint(os.Stderr, "lock retry budget exhausted")
	os.Exit(3)
}

func sharedCreateSubprocess(t *testing.T, board, id string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSharedCreateHelper", "--", board, id)
	cmd.Env = append(os.Environ(), "TASKSTORE_SHARED_HELPER=1")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("helper %s: %w: %s", filepath.Base(board), err, raw)
	}
	return strings.TrimSpace(string(raw)), nil
}

func TestSharedCreateSubprocessesReserveUniqueAutomaticIDs(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct {
		id  string
		err error
	}, 2)
	for _, board := range []string{a, b} {
		go func(board string) {
			id, err := sharedCreateSubprocess(t, board, "-")
			ch <- struct {
				id  string
				err error
			}{id, err}
		}(board)
	}
	first, second := <-ch, <-ch
	if first.err != nil {
		t.Fatal(first.err)
	}
	if second.err != nil {
		t.Fatal(second.err)
	}
	if first.id == second.id {
		t.Fatalf("duplicate automatic IDs: %s", first.id)
	}
	if (first.id != "TASK-2" && first.id != "TASK-3") || (second.id != "TASK-2" && second.id != "TASK-3") {
		t.Fatalf("unexpected allocation: %s %s", first.id, second.id)
	}
}

func TestSharedCreateSubprocessSameAliasOnlyOneSucceeds(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	ch := make(chan error, 2)
	for i, board := range []string{a, b} {
		id := "TASK-500"
		if i == 1 {
			id = "TASK-0500"
		}
		go func(board, id string) { _, err := sharedCreateSubprocess(t, board, id); ch <- err }(board, id)
	}
	first, second := <-ch, <-ch
	if (first == nil) == (second == nil) {
		t.Fatalf("alias outcomes = %v, %v; exactly one should succeed", first, second)
	}
	failure := first
	if failure == nil {
		failure = second
	}
	if !strings.Contains(failure.Error(), "already reserved") {
		t.Fatalf("unexpected race failure: %v", failure)
	}
}

func TestSharedCreateSubprocessDistinctExplicitHolesBothSucceed(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := ReserveIDs(a, []string{"TASK-1000"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	ch := make(chan error, 2)
	for i, board := range []string{a, b} {
		id := "TASK-777"
		if i == 1 {
			id = "TASK-778"
		}
		go func(board, id string) { _, err := sharedCreateSubprocess(t, board, id); ch <- err }(board, id)
	}
	if err := <-ch; err != nil {
		t.Fatal(err)
	}
	if err := <-ch; err != nil {
		t.Fatal(err)
	}
}
