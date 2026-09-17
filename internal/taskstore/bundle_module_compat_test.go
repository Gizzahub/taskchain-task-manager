package taskstore

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestModuleBundleIntermediateBinaryBarrier(t *testing.T) {
	binary := os.Getenv("TASKCHAIN_MODULE_BUNDLE_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_MODULE_BUNDLE_LEGACY_BINARY to module-aware pre-destination executable")
	}
	for _, phase := range []string{"after-pending-journal", "after-card-0", "after-completed-receipt"} {
		t.Run(phase, func(t *testing.T) {
			board := moduleAdoptionBoard(t)
			if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
				t.Fatal(err)
			}
			raw := moduleBundleRequest(t, board, "backend", "auth/api")
			if out, err := runLegacyPolicyCommand(binary, "list", "--dir", board, "--json"); err != nil {
				t.Fatalf("module-aware baseline failed: %s %v", out, err)
			}
			stop := errors.New("saved module bundle")
			if _, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			before := boardBytes(t, board)
			for _, args := range [][]string{
				{"list", "--dir", board, "--json"},
				{"create", "--dir", board, "--title", "old", "--json"},
				{"reserve-ids", "--dir", board, "--id", "TASK-999", "--json"},
			} {
				out, err := runLegacyPolicyCommand(binary, args...)
				var exit *exec.ExitError
				unknownScope := strings.Contains(string(out), "unknown field bundle.tasks[0].module") || strings.Contains(string(out), "unknown field bundle.tasks[0].category")
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || !unknownScope {
					t.Fatalf("intermediate writer not fenced: %v %s %v", args, out, err)
				}
				if !reflect.DeepEqual(before, boardBytes(t, board)) {
					t.Fatal("intermediate writer mutated board")
				}
			}
		})
	}
}
