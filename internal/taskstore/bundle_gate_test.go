package taskstore

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func installBundleTestJournal(t *testing.T, dir string, records []bundleRecord, marker bool) {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := saveBundles(r, bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("b", 32), Records: records}, true); err != nil {
		t.Fatal(err)
	}
	if marker {
		j, err := loadTransitionsForBundle(r)
		if err != nil {
			t.Fatal(err)
		}
		j.BundleProtocol = 1
		if err := publishTransitionJournal(r, j); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBundleGateRejectsIncompleteAdoption(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"orphan", "missing", "corrupt", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			// A completed receipt must not provide a bypass around the bundle gate.
			if _, err := Transition(dir, req); err != nil {
				t.Fatal(err)
			}
			records := []bundleRecord{}
			if scenario == "pending" {
				records = append(records, bundleRecordFixture(t))
			}
			installBundleTestJournal(t, dir, records, scenario != "orphan")
			r, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				err = r.Remove(bundlesFile)
			case "corrupt":
				err = r.WriteFile(bundlesFile, []byte("invalid"), 0o600)
			}
			if closeErr := r.Close(); err != nil || closeErr != nil {
				t.Fatal(errors.Join(err, closeErr))
			}
			before := boardBytes(t, dir)
			claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}
			checks := map[string]func() error{
				"init":              func() error { return Init(dir) },
				"list":              func() error { _, err := List(dir); return err },
				"ready":             func() error { _, err := Ready(dir); return err },
				"create":            func() error { _, err := Create(dir, CreateRequest{Title: "Blocked"}); return err },
				"reserve":           func() error { _, err := ReserveIDs(dir, []string{"TASK-88"}, true); return err },
				"claim":             func() error { _, err := Claim(dir, claim); return err },
				"release":           func() error { _, err := Release(dir, claim); return err },
				"resume":            func() error { _, err := ClaimResume(dir, claim); return err },
				"transition-replay": func() error { _, err := Transition(dir, req); return err },
				"recover-replay":    func() error { _, err := Recover(dir, req); return err },
				"register-context":  func() error { _, err := RegisterContext(dir, []byte(testContextIntent)); return err },
			}
			intentReq, _ := bundleFixture(t)
			checks["show-context"] = func() error { _, err := ShowContext(dir, "intent", intentReq.Batch.Intent.ID, 1); return err }
			for name, check := range checks {
				err := check()
				if err == nil {
					t.Fatalf("%s bypassed %s bundle gate", name, scenario)
				}
				if scenario == "pending" && !strings.Contains(err.Error(), "pending bundle") {
					t.Fatalf("%s failed outside bundle gate: %v", name, err)
				}
				if !reflect.DeepEqual(before, boardBytes(t, dir)) {
					t.Fatalf("%s changed blocked board", name)
				}
			}
		})
	}
}

func TestBundleMarkerSurvivesTransitionAndRecovery(t *testing.T) {
	t.Parallel()
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "recovery"}[interrupted], func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			installBundleTestJournal(t, dir, []bundleRecord{}, true)
			stop := errors.New("synthetic interruption")
			_, err := transitionWithStep(dir, req, func(at string) error {
				if interrupted && at == "after-journal" {
					return stop
				}
				return nil
			})
			if interrupted {
				if !errors.Is(err, stop) {
					t.Fatal(err)
				}
				_, err = Recover(dir, req)
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			j, err := loadTransitions(r)
			if err != nil || j.BundleProtocol != 1 {
				t.Fatalf("marker lost: %+v %v", j, err)
			}
			if _, err := List(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBundleAdoptedBoardCannotReinitializeMissingIDs(t *testing.T) {
	t.Parallel()
	dir := configuredFixture(t)
	record := bundleRecordFixture(t)
	record.Status = "completed"
	installBundleTestJournal(t, dir, []bundleRecord{record}, true)
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(idsFile); err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := ReserveIDs(dir, []string{}, true); err == nil {
		t.Fatal("adopted board reinitialized missing IDs")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("missing ledger recovery changed board")
	}
}
