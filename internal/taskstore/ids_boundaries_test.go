package taskstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCorruptIDLedgerFailsClosed(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string][]byte{
		"duplicate key": []byte(`{"schemaVersion":1,"reserved":[],"reserved":[]}`),
		"unknown":       []byte(`{"schemaVersion":1,"reserved":[],"extra":1}`),
		"case":          []byte(`{"schemaVersion":1,"Reserved":[]}`),
		"version":       []byte(`{"schemaVersion":3,"reserved":[]}`),
		"null":          []byte(`{"schemaVersion":1,"reserved":null}`),
		"duplicate ID":  []byte(`{"schemaVersion":1,"reserved":["TASK-1","TASK-1"]}`),
		"unsorted":      []byte(`{"schemaVersion":1,"reserved":["TASK-2","TASK-1"]}`),
		"leading zero":  []byte(`{"schemaVersion":1,"reserved":["TASK-01"]}`),
		"overflow":      []byte(`{"schemaVersion":1,"reserved":["TASK-18446744073709551616"]}`),
		"null ID":       []byte(`{"schemaVersion":1,"reserved":[null]}`),
		"trailing":      []byte(`{"schemaVersion":1,"reserved":[]} {}`),
		"invalid utf8":  []byte{'{', 0xff, '}'},
		"too large":     []byte(strings.Repeat(" ", maxIDsBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tasks")
			if err := Init(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, idsFile), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			checks := []func() error{
				func() error { return Init(dir) },
				func() error { _, e := List(dir); return e },
				func() error { _, e := Create(dir, CreateRequest{Title: "no"}); return e },
				func() error { _, e := ReserveIDs(dir, []string{"TASK-1"}, true); return e },
			}
			for _, check := range checks {
				if err := check(); err == nil {
					t.Fatal("corrupt ledger accepted")
				}
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("corrupt ledger overwritten")
			}
		})
	}
}

func TestIDLedgerNonRegularRejected(t *testing.T) {
	t.Parallel()
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tasks")
			if err := Init(dir); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, idsFile)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			var err error
			if symlink {
				err = os.Symlink("todo", path)
			} else {
				err = os.Mkdir(path, 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			if _, err := ReserveIDs(dir, nil, true); err == nil {
				t.Fatal("nonregular ledger accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("nonregular ledger replaced")
			}
		})
	}
}

func TestIDCapacityRefusesBeforeReservationOrCard(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	j := idLedger{SchemaVersion: 1, Reserved: []string{}}
	empty, _ := json.Marshal(j)
	count := (maxIDsBytes - len(empty) - 1) / 28
	for i := 0; i < count; i++ {
		j.Reserved = append(j.Reserved, fmt.Sprintf("TASK-%d", uint64(18000000000000000000)+uint64(i)))
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := publishIDs(r, j, false); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Create(dir, CreateRequest{Title: "too much"}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("capacity err=%v", err)
	}
	if _, err := ReserveIDs(dir, []string{"TASK-18446744073709551615"}, false); err == nil {
		t.Fatal("reservation bypassed capacity")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("capacity failure changed board")
	}
}

func TestDeletedReceiptIDsRemainReserved(t *testing.T) {
	t.Parallel()
	for _, omit := range []string{"", claimsFile, transitionsFile} {
		t.Run("omit-"+omit, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			if _, err := Transition(dir, req); err != nil {
				t.Fatal(err)
			}
			if _, err := Release(dir, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "doing/custom-name.md")); err != nil {
				t.Fatal(err)
			}
			if omit != "" {
				if err := os.Remove(filepath.Join(dir, omit)); err != nil {
					t.Fatal(err)
				}
			}
			e, err := Create(dir, CreateRequest{Title: "after deleted manual card"})
			if err != nil || e.Card.ID != "TASK-2" {
				t.Fatalf("receipt ID reused: %v %v", e, err)
			}
		})
	}
}
