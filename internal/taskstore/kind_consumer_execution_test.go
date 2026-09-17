package taskstore

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestKindConsumerExecutionRequiresExactClaimAndDoneDependencies(t *testing.T) {
	for _, targetZone := range []string{"doing", "done"} {
		t.Run(targetZone, func(t *testing.T) {
			board := filepath.Join(t.TempDir(), "tasks")
			depDone := "backend/done/auth/api/TASK-1.md"
			depPlan := "backend/plan/auth/api/TASK-1.md"
			depTodo := "backend/todo/auth/api/TASK-1.md"
			writeModuleCard(t, board, depDone, "TASK-1", "done")
			if _, err := ActivatePolicy(board, kindConsumerPolicy(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
				t.Fatal(err)
			}
			entry, err := Create(board, CreateRequest{Title: "execution", Module: "backend", Category: "auth/api", DependsOn: []string{"TASK-1"}})
			if err != nil {
				t.Fatal(err)
			}
			claim := ClaimRequest{ID: entry.Card.ID, Owner: "consumer", Token: testToken}
			if _, err := Claim(board, claim); err != nil {
				t.Fatal(err)
			}
			plan := strings.Replace(entry.Path, "/todo/", "/plan/", 1)
			park := kindConsumerMove(t, board, entry.Card.ID, entry.Path, plan, 'a')
			park.Token = claim.Token
			if _, err := Relocate(board, park, true); err != nil {
				t.Fatal(err)
			}
			if _, err := Relocate(board, kindConsumerMove(t, board, "TASK-1", depDone, depPlan, 'b'), true); err != nil {
				t.Fatal(err)
			}
			req := kindConsumerMove(t, board, entry.Card.ID, plan, strings.Replace(plan, "/plan/", "/"+targetZone+"/", 1), 'c')
			req.Token = claim.Token
			for _, variant := range []string{"missing-token", "wrong-owner", "wrong-token", "dependency"} {
				bad := req
				switch variant {
				case "missing-token":
					bad.Token = ""
				case "wrong-owner":
					bad.Owner = "another"
				case "wrong-token":
					bad.Token = strings.Repeat("f", 32)
				}
				want := "exact held claim"
				if variant == "dependency" {
					want = "relocation dependency is not done"
				}
				before := boardBytes(t, board)
				if _, err := Relocate(board, bad, true); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("%s: wrong admission result %v", variant, err)
				}
				if !reflectEqualBoard(before, boardBytes(t, board)) {
					t.Fatalf("%s refusal changed board", variant)
				}
			}
			if _, err := Relocate(board, kindConsumerMove(t, board, "TASK-1", depPlan, depTodo, 'd'), true); err != nil {
				t.Fatal(err)
			}
			depClaim := ClaimRequest{ID: "TASK-1", Owner: "consumer", Token: strings.Repeat("e", 32)}
			if _, err := Claim(board, depClaim); err != nil {
				t.Fatal(err)
			}
			from := "todo"
			for i, to := range []string{"doing", "review", "done"} {
				if _, err := Transition(board, TransitionRequest{ID: depClaim.ID, Owner: depClaim.Owner, Token: depClaim.Token, RequestID: fmt.Sprintf("%032x", i+1), From: from, To: to}); err != nil {
					t.Fatal(err)
				}
				from = to
			}
			result, err := Relocate(board, req, true)
			if err != nil || result.Target != req.Target || result.Status != "completed" {
				t.Fatalf("same request after dependency completion failed: %+v %v", result, err)
			}
		})
	}
}
