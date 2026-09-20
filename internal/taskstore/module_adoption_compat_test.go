package taskstore

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

func TestModuleAdoptionLegacyBinaryBarrier(t *testing.T) {
	t.Parallel()
	binary := os.Getenv("TASKCHAIN_MODULE_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_MODULE_LEGACY_BINARY to verified pre-module executable")
	}
	for _, shared := range []bool{false, true} {
		points := []string{"after-local-pending", "after-policy-ids", "after-local-completed"}
		if shared {
			points = []string{"after-common-policy-pending", "board-0/after-policy-ids", "after-policy-board-0", "after-common-policy-active"}
		}
		for _, point := range points {
			t.Run(point, func(t *testing.T) {
				var a, b string
				var options PolicyRevisionOptions
				if shared {
					_, a, b, options = sharedRevisionFixture(t)
				} else {
					a, options = localRevisionFixture(t)
				}
				if out, err := runLegacyPolicyCommand(binary, "list", "--dir", a, "--json"); err != nil {
					t.Fatalf("old binary cannot read pre-module baseline: %s %v", out, err)
				}
				writeModuleCard(t, a, "backend/todo/TASK-100.md", "TASK-100", "pending")
				options.AdoptModules = true
				stop := errors.New("module adoption boundary")
				if _, err := revisePolicyWithStep(a, moduleAdoptionRaw(t), options, func(at string) error {
					if at == point {
						return stop
					}
					return nil
				}); !errors.Is(err, stop) {
					t.Fatalf("boundary not reached: %v", err)
				}
				common := policyCommonBytes(t, a)
				for _, dir := range []string{a, b} {
					if dir == "" {
						continue
					}
					before := boardBytes(t, dir)
					for _, args := range [][]string{
						{"list", "--dir", dir, "--json"},
						{"create", "--dir", dir, "--title", "old writer", "--json"},
						{"reserve-ids", "--dir", dir, "--id", "TASK-999", "--json"},
					} {
						out, err := runLegacyPolicyCommand(binary, args...)
						var exit *exec.ExitError
						if !errors.As(err, &exit) || exit.ExitCode() != 1 {
							t.Fatalf("old command not rejected: %v: %s %v", args, out, err)
						}
						if !reflect.DeepEqual(before, boardBytes(t, dir)) {
							t.Fatal("old command changed board")
						}
					}
				}
				if !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
					t.Fatal("old command changed common state")
				}
			})
		}
	}
}
