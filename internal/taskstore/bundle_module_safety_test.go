package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModuleBundleRejectsUnsafePublicationBeforeReservation(t *testing.T) {
	for _, scenario := range []string{"component-length", "path-length", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			board := moduleAdoptionBoard(t)
			if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
				t.Fatal(err)
			}
			req, _ := bundleFixture(t)
			module, category := "backend", "alias/child"
			outside := t.TempDir()
			switch scenario {
			case "component-length":
				category = strings.Repeat("x", 256)
			case "path-length":
				category = strings.Repeat(strings.Repeat("x", 200)+"/", 5) + "end"
			case "symlink":
				if err := os.Symlink(outside, filepath.Join(board, "backend/todo/alias")); err != nil {
					t.Fatal(err)
				}
			}
			req.Tasks[0].Module, req.Tasks[0].Category = &module, &category
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, board)
			if _, err := PublishBundle(board, raw, BundleOptions{Adopt: true}); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("unsafe request changed board or reservation")
			}
			files, err := os.ReadDir(outside)
			if err != nil || len(files) != 0 {
				t.Fatal("unsafe request changed symlink target")
			}
		})
	}
}
