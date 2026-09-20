package taskstore

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func revisionPolicyBytes(t *testing.T) []byte {
	t.Helper()
	p, err := boardpolicy.New(boardpolicy.Declaration{Transitions: []boardpolicy.Transition{{From: "todo", To: []string{"done"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func localRevisionFixture(t *testing.T) (string, PolicyRevisionOptions) {
	t.Helper()
	dir := claimBoard(t)
	active, err := ActivatePolicy(dir, defaultPolicyBytes(t), PolicyActivationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return dir, PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest}
}

func TestPolicyRevisionLocalPublicationRecovery(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-local-pending", "after-policy", "after-policy-ids", "after-policy-journal", "after-local-completed"} {
		t.Run(phase, func(t *testing.T) {
			dir, options := localRevisionFixture(t)
			raw := revisionPolicyBytes(t)
			stop := errors.New("synthetic revision interruption")
			if _, err := revisePolicyWithStep(dir, raw, options, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("boundary not reached: %v", err)
			}
			if phase != "after-local-completed" {
				before := boardBytes(t, dir)
				if _, err := Create(dir, CreateRequest{Title: "blocked"}); err == nil {
					t.Fatal("pending revision admitted writer")
				}
				if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{Resume: true}); err == nil {
					t.Fatal("initial activation resumed revision")
				}
				if _, err := RevisePolicy(dir, raw, options); err == nil {
					t.Fatal("implicit revision resume")
				}
				if !reflect.DeepEqual(before, boardBytes(t, dir)) {
					t.Fatal("rejected requests changed board")
				}
			}
			options.Resume = true
			result, err := RevisePolicy(dir, raw, options)
			if err != nil || result.Status != "completed" || result.AuthorityID == options.ExpectedAuthorityID {
				t.Fatalf("resume=%+v %v", result, err)
			}
			if _, err := List(dir); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			replayed, err := RevisePolicy(dir, raw, options)
			if err != nil || !replayed.Replayed {
				t.Fatalf("replay=%+v %v", replayed, err)
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("completed replay mutated board")
			}
		})
	}
}

func TestPolicyRevisionCASAndStaleReplay(t *testing.T) {
	t.Parallel()
	dir, options := localRevisionFixture(t)
	raw := revisionPolicyBytes(t)
	for _, bad := range []PolicyRevisionOptions{{}, {ExpectedAuthorityID: strings.Repeat("f", 32), ExpectedDigest: options.ExpectedDigest}, {ExpectedAuthorityID: options.ExpectedAuthorityID, ExpectedDigest: strings.Repeat("f", 64)}, {ExpectedAuthorityID: options.ExpectedAuthorityID, ExpectedDigest: options.ExpectedDigest, Resume: true}} {
		before := boardBytes(t, dir)
		if _, err := RevisePolicy(dir, raw, bad); err == nil {
			t.Fatal("invalid CAS admitted")
		}
		if !reflect.DeepEqual(before, boardBytes(t, dir)) {
			t.Fatal("invalid CAS mutated")
		}
	}
	first, err := RevisePolicy(dir, raw, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RevisePolicy(dir, defaultPolicyBytes(t), PolicyRevisionOptions{ExpectedAuthorityID: first.AuthorityID, ExpectedDigest: first.Digest})
	if err != nil || second.AuthorityID == options.ExpectedAuthorityID {
		t.Fatalf("return revision=%+v %v", second, err)
	}
	before := boardBytes(t, dir)
	if _, err := RevisePolicy(dir, raw, options); err == nil {
		t.Fatal("stale CAS reapplied after policy returned")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("stale request mutated")
	}
}

func TestPolicyRevisionPreservesHistoricalTransition(t *testing.T) {
	t.Parallel()
	dir, req, _, _ := transitionFixture(t)
	claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}
	if _, err := Release(dir, claim); err != nil {
		t.Fatal(err)
	}
	active, err := ActivatePolicy(dir, defaultPolicyBytes(t), PolicyActivationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req.Token = strings.Repeat("e", 32)
	claim.Token = req.Token
	if _, err := Claim(dir, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := Transition(dir, req); err != nil {
		t.Fatal(err)
	}
	if _, err := Release(dir, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := RevisePolicy(dir, revisionPolicyBytes(t), PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest}); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Transition(dir, req); err != nil {
		t.Fatalf("historical receipt replay: %v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("historical replay wrote files")
	}
}

func TestPolicyRevisionEnablesV2Relocation(t *testing.T) {
	t.Parallel()
	dir, options := localRevisionFixture(t)
	if _, err := RevisePolicy(dir, []byte(relocationPolicyFixture), options); err != nil {
		t.Fatal(err)
	}
	req := relocationRequestFor(t, dir)
	if _, err := Relocate(dir, req, true); err != nil {
		t.Fatalf("revised v1 board cannot use v2 relocation: %v", err)
	}
	if _, err := RecoverRelocation(dir, req); err != nil {
		t.Fatal(err)
	}
}
