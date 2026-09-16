package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfiguredCreateUsesCommonReservationFailurePath(t *testing.T) {
	for _, mode := range []string{"local", "shared"} {
		t.Run(mode, func(t *testing.T) {
			var board, other, reserved, next, point string
			if mode == "local" {
				board = configuredFixture(t)
				other, reserved, next, point = board, "TASK-1", "TASK-2", "after-reservation"
			} else {
				_, board, other = sharedFixture(t)
				if _, err := EnableShared(board, false); err != nil {
					t.Fatal(err)
				}
				reserved, next, point = "TASK-2", "TASK-3", "after-shared-reservation"
			}
			stop := errors.New("synthetic configured publish interruption")
			_, err := createWithStep(board, CreateRequest{Title: "Interrupted", Template: configuredTemplate()}, func(at string) error {
				if at == point {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) || !strings.Contains(err.Error(), reserved+" is reserved") {
				t.Fatalf("error=%v", err)
			}
			if _, err := os.Stat(filepath.Join(board, "todo", reserved+".md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("card published: %v", err)
			}
			entry, err := Create(other, CreateRequest{Title: "Next", Template: configuredTemplate()})
			if err != nil || entry.Card.ID != next {
				t.Fatalf("next=%+v err=%v", entry, err)
			}
		})
	}
}

func TestConfiguredInvalidInputsLeaveBoardBytesUntouched(t *testing.T) {
	board := configuredFixture(t)
	before := boardBytes(t, board)
	for name, change := range map[string]func(*CreateRequest){
		"invalid utf8":       func(r *CreateRequest) { r.Title = string([]byte{0xff}) },
		"empty type":         func(r *CreateRequest) { r.Template.Type = "" },
		"invalid priority":   func(r *CreateRequest) { r.Template.Priority = "P9" },
		"summary newline":    func(r *CreateRequest) { r.Template.Summary = "a\nb" },
		"criterion newline":  func(r *CreateRequest) { r.Template.Criteria = []string{"a\nb"} },
		"criterion empty":    func(r *CreateRequest) { r.Template.Criteria = []string{" "} },
		"criterion absent":   func(r *CreateRequest) { r.Template.Criteria = nil },
		"wrong ID kind":      func(r *CreateRequest) { r.ID = "PLAN-1" },
		"wrong kind":         func(r *CreateRequest) { r.Kind = "issue" },
		"invalid rules":      func(r *CreateRequest) { r.Template.Rules.CriteriaHeading = "Not a criteria heading" },
		"oversized output":   func(r *CreateRequest) { r.Template.Summary = strings.Repeat("a", 1<<20) },
		"missing dependency": func(r *CreateRequest) { r.DependsOn = []string{"TASK-999"} },
	} {
		t.Run(name, func(t *testing.T) {
			req := CreateRequest{Title: "Valid", Template: configuredTemplate()}
			change(&req)
			if _, err := Create(board, req); err == nil {
				t.Fatal("invalid configured request accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("invalid input changed board or reservations")
			}
		})
	}
}
