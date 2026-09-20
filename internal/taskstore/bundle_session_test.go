package taskstore

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestBundleSessionAdoptionRequiresExplicitUpgrade(t *testing.T) {
	t.Parallel()
	dir := configuredFixture(t)
	before := boardBytes(t, dir)
	s, err := openBundleSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = s.adopt(false, nil)
	closeErr := s.close()
	if err == nil || closeErr != nil {
		t.Fatalf("adopt=%v close=%v", err, closeErr)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("unapproved adoption changed board")
	}
	s, err = openBundleSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = s.adopt(true, nil)
	closeErr = s.close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	if _, err := List(dir); err != nil {
		t.Fatal(err)
	}
	s, err = openBundleSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.adopt(false, nil); err != nil {
		t.Fatalf("already adopted: %v", err)
	}
}

func TestBundleSessionResumesAdoptionInterruption(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-empty-journal", "after-local-protocol", "after-common-protocol"} {
		t.Run(phase, func(t *testing.T) {
			_, dir, _ := sharedFixture(t)
			if _, err := EnableShared(dir, false); err != nil {
				t.Fatal(err)
			}
			s, err := openBundleSession(dir)
			if err != nil {
				t.Fatal(err)
			}
			boardID := s.journal.BoardID
			stop := errors.New("synthetic interruption")
			err = s.adopt(true, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			})
			closeErr := s.close()
			if !errors.Is(err, stop) || closeErr != nil {
				t.Fatalf("phase=%v close=%v", err, closeErr)
			}
			if phase == "after-empty-journal" {
				if _, err := List(dir); err == nil {
					t.Fatal("orphan journal accepted by ordinary reader")
				}
			}
			s, err = openBundleSession(dir)
			if err != nil {
				t.Fatal(err)
			}
			if s.journal.BoardID != boardID {
				s.close()
				t.Fatal("adoption retry replaced board identity")
			}
			err = s.adopt(true, nil)
			if s.transitions.BundleProtocol != 1 || s.shared.state.BundleProtocol != 1 || s.shared.state.SchemaVersion != 2 {
				s.close()
				t.Fatal("protocol upgrade incomplete")
			}
			closeErr = s.close()
			if err != nil || closeErr != nil {
				t.Fatal(errors.Join(err, closeErr))
			}
			if _, err := List(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBundleSessionCannotBypassPendingTransition(t *testing.T) {
	t.Parallel()
	dir, req, _, _ := transitionFixture(t)
	stop := errors.New("synthetic interruption")
	_, err := transitionWithStep(dir, req, func(at string) error {
		if at == "after-journal" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	s, err := openBundleSession(dir)
	if err == nil {
		s.close()
		t.Fatal("bundle session bypassed pending transition")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("failed session changed pending transition")
	}
}

func TestBundleIdentityRejectsBoardDifferentFromLockedLocation(t *testing.T) {
	t.Parallel()
	_, owner, other := sharedFixture(t)
	s, release, err := acquireSharedForBundle(owner, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r, err := os.OpenRoot(other)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before := boardBytes(t, other)
	if err := s.verifyBoardIdentity(r); err == nil {
		t.Fatal("different board accepted under owner common session")
	}
	if !reflect.DeepEqual(before, boardBytes(t, other)) {
		t.Fatal("identity failure modified board")
	}
}
