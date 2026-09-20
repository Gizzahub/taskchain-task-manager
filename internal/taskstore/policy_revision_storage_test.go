package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPolicyRevisionPreservesStorageReceipts(t *testing.T) {
	t.Parallel()
	dir, _ := relocationBoardFixture(t)
	active, err := ActivatePolicy(dir, []byte(relocationPolicyFixture), PolicyActivationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repair := storageRepairRequest(t, dir, 'b')
	if _, err := RepairStatus(dir, repair, true); err != nil {
		t.Fatal(err)
	}
	relocation := relocationRequestFor(t, dir)
	if _, err := Relocate(dir, relocation, true); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, name := range []string{repairsFile, relocationsFile, relocation.Target} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = raw
	}
	if _, err := RevisePolicy(dir, defaultPolicyBytes(t), PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest}); err != nil {
		t.Fatal(err)
	}
	for name, expected := range before {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(raw, expected) {
			t.Fatalf("revision changed %s: %v", name, err)
		}
	}
	boardBefore := boardBytes(t, dir)
	if _, err := RecoverStatusRepair(dir, repair); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverRelocation(dir, relocation); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(boardBefore, boardBytes(t, dir)) {
		t.Fatal("historical storage replay changed board")
	}
}

func TestPolicyRevisionRejectsPendingRelocation(t *testing.T) {
	t.Parallel()
	dir, req := relocationBoardFixture(t)
	active, err := ActivatePolicy(dir, []byte(relocationPolicyFixture), PolicyActivationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relocateWithStep(dir, req, true, false, repairStopAt("after-relocation-journal")); err == nil || !strings.Contains(err.Error(), "stop at after-relocation-journal") {
		t.Fatalf("fixture did not reach durable boundary: %v", err)
	}
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := loadRelocationJournal(r)
	if closeErr := r.Close(); err != nil || closeErr != nil {
		t.Fatalf("pending journal read: %v %v", err, closeErr)
	}
	if len(journal.Records) != 1 || journal.Records[0].Kind != "pending" {
		t.Fatal("fixture has no pending relocation")
	}
	before := boardBytes(t, dir)
	if _, err := RevisePolicy(dir, defaultPolicyBytes(t), PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest}); err == nil {
		t.Fatal("pending relocation admitted revision")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("rejected revision changed pending relocation")
	}
}
