package taskstore

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestContextRegistryPreservesPendingTransition(t *testing.T) {
	board, req, _, _ := transitionFixture(t)
	registered, err := RegisterContext(board, []byte(testContextIntent))
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("synthetic transition interruption")
	_, err = transitionWithStep(board, req, func(at string) error {
		if at == "after-journal" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	_, registerErr := RegisterContext(board, []byte(testContextIntent))
	_, showErr := ShowContext(board, registered.Kind, registered.ID, registered.Revision)
	for _, err := range []error{registerErr, showErr} {
		if err == nil || !strings.Contains(err.Error(), "pending") {
			t.Fatalf("pending gate error: %v", err)
		}
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("registry operation changed pending transition")
	}
	if _, err := Recover(board, req); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
}

func TestContextRegistryUsesSharedSession(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../examples/context/intent.json")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := RegisterContext(b, raw)
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, b)
	_, registerErr := RegisterContext(b, raw)
	_, showErr := ShowContext(b, registered.Kind, registered.ID, registered.Revision)
	closeErr := release()
	assertLocked(t, registerErr)
	assertLocked(t, showErr)
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if !reflect.DeepEqual(before, boardBytes(t, b)) {
		t.Fatal("locked registry operations changed board")
	}
	replayed, err := RegisterContext(b, raw)
	if err != nil || replayed.Status != "unchanged" {
		t.Fatalf("replay after release = %+v, %v", replayed, err)
	}
	if _, err := ShowContext(b, registered.Kind, registered.ID, registered.Revision); err != nil {
		t.Fatal(err)
	}
}
