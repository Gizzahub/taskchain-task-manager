package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestReadActivePolicyUsesBoundAuthorityWithoutWrites(t *testing.T) {
	dir := claimBoard(t)
	state := bindAuthorityFixture(t, dir, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"})
	before := boardBytes(t, dir)
	policy, err := ReadActivePolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := policy.Canonical()
	if err != nil || string(canonical) != string(state.Canonical) {
		t.Fatalf("ReadActivePolicy returned an unbound policy: %q %v", canonical, err)
	}
	if after := boardBytes(t, dir); !reflect.DeepEqual(after, before) {
		t.Fatal("read-only policy lookup changed board bytes")
	}
}

func TestReadActivePolicyRejectsWriterLockAndBrokenBinding(t *testing.T) {
	dir := claimBoard(t)
	bindAuthorityFixture(t, dir, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"})

	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadActivePolicy(dir); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("ReadActivePolicy lock error = %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, policyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadActivePolicy(dir); err == nil || !strings.Contains(err.Error(), "bound policy") {
		t.Fatalf("ReadActivePolicy accepted missing authority policy: %v", err)
	}
}

func TestReadActivePolicyRejectsPendingSharedAuthority(t *testing.T) {
	_, owner, _ := sharedFixture(t)
	if _, err := EnableShared(owner, false); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop after common policy pending")
	if _, err := activatePolicyWithStep(owner, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
		if at == "after-common-policy-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("prepare pending shared authority: %v", err)
	}
	if _, err := ReadActivePolicy(owner); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("ReadActivePolicy accepted pending shared authority: %v", err)
	}
}

func TestTargetFileRoleAdmissionRejectsUnknownBeforePreparation(t *testing.T) {
	dir := claimBoard(t)
	if err := os.WriteFile(filepath.Join(dir, "unexpected.bin"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	j, err := loadTransitions(r)
	if err != nil {
		t.Fatal(err)
	}
	target, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{}, FileRolesDeclared: true, FileRoles: []boardpolicy.FileRole{}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = preparePolicyChangeWithModules(r, target, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}, "", j, nil, false)
	if err == nil || !strings.Contains(err.Error(), "file-role admission") {
		t.Fatalf("target admission error = %v", err)
	}
	if _, err := r.Lstat(policyFile); !os.IsNotExist(err) {
		t.Fatalf("target preflight wrote policy file: %v", err)
	}
}
