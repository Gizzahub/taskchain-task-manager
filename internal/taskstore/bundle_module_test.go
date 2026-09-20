package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModuleBundlePublicationRecoveryAndReplay(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-pending-journal", "after-local-reservation", "after-card-0", "after-completed-receipt"} {
		t.Run(phase, func(t *testing.T) {
			board := moduleAdoptionBoard(t)
			if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
				t.Fatal(err)
			}
			req, _ := bundleFixture(t)
			module, category := "backend", "auth/api"
			req.Tasks[0].Module, req.Tasks[0].Category = &module, &category
			raw, err := parsedBundle(t, req).Canonical()
			if err != nil {
				t.Fatal(err)
			}
			stop := errors.New("module bundle stop")
			if _, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("boundary: %v", err)
			}
			result, err := PublishBundle(board, raw, BundleOptions{Adopt: true, Resume: true})
			if err != nil || len(result.Tasks) != 1 || result.Tasks[0].Path != "backend/todo/auth/api/TASK-8.md" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if _, err := List(board); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(board, result.Tasks[0].Path)); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, board)
			if replay, err := PublishBundle(board, raw, BundleOptions{}); err != nil || !replay.Replayed {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("completed replay recreated module card")
			}
		})
	}
}
