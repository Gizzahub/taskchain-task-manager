package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func moduleBundleRequest(t *testing.T, board, module, category string) []byte {
	t.Helper()
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	req, _ := bundleFixture(t)
	req.Tasks[0].Module, req.Tasks[0].Category = &module, &category
	raw, err := parsedBundle(t, req).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestModuleBundleSharedRecovery(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-shared-reservation", "after-local-reservation", "after-card-0"} {
		t.Run(phase, func(t *testing.T) {
			_, board, other := sharedFixture(t)
			if _, err := EnableShared(board, false); err != nil {
				t.Fatal(err)
			}
			if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AllWorktrees: true, AdoptModules: true}); err != nil {
				t.Fatal(err)
			}
			raw := moduleBundleRequest(t, board, "backend", "auth/api")
			s, release, err := acquireSharedForPolicy(board)
			if err != nil {
				t.Fatal(err)
			}
			commonPath := filepath.Join(s.root.Name(), sharedStateFile)
			if err := release(); err != nil {
				t.Fatal(err)
			}
			readCommon := func() []byte {
				raw, err := os.ReadFile(commonPath)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			stop := errors.New("shared module bundle boundary")
			if _, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			beforeOther, common := boardBytes(t, other), readCommon()
			if _, err := Create(other, CreateRequest{Title: "blocked", Module: "backend"}); err == nil {
				t.Fatal("other writer bypassed shared bundle")
			}
			if !reflect.DeepEqual(beforeOther, boardBytes(t, other)) || !reflect.DeepEqual(common, readCommon()) {
				t.Fatal("blocked writer changed shared state")
			}
			result, err := PublishBundle(board, raw, BundleOptions{Resume: true})
			if err != nil || len(result.Tasks) != 1 || result.Tasks[0].Path != "backend/todo/auth/api/TASK-2.md" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			next, err := Create(other, CreateRequest{Title: "next", Module: "backend"})
			if err != nil || next.Card.ID != "TASK-3" {
				t.Fatalf("next=%+v err=%v", next, err)
			}
		})
	}
}

func TestModuleBundleScopeRefusalDoesNotAdoptProtocol(t *testing.T) {
	t.Parallel()
	for _, scope := range []struct{ module, category string }{{"unknown", "auth"}, {"todo", "auth"}, {"backend", "done"}} {
		board := moduleAdoptionBoard(t)
		if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
			t.Fatal(err)
		}
		raw := moduleBundleRequest(t, board, scope.module, scope.category)
		before := boardBytes(t, board)
		if _, err := PublishBundle(board, raw, BundleOptions{Adopt: true}); err == nil {
			t.Fatalf("accepted %+v", scope)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatalf("scope refusal changed board: %+v", scope)
		}
	}
}
