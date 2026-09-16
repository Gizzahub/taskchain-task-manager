package taskstore

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestPolicyRevisionLegacyBinaryBarrier(t *testing.T) {
	binary := os.Getenv("TASKCHAIN_REVISION_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_REVISION_LEGACY_BINARY to pre-revision executable")
	}
	for _, shared := range []bool{false, true} {
		points := []string{"after-local-pending", "after-policy", "after-policy-journal", "after-local-completed"}
		if shared {
			points = []string{"after-common-policy-pending", "board-0/after-policy-journal", "after-common-policy-active"}
		}
		for _, point := range points {
			t.Run(strings.Join([]string{map[bool]string{false: "local", true: "shared"}[shared], point}, "/"), func(t *testing.T) {
				var a, b string
				var options PolicyRevisionOptions
				if shared {
					_, a, b, options = sharedRevisionFixture(t)
				} else {
					a, options = localRevisionFixture(t)
				}
				if out, err := runLegacyPolicyCommand(binary, "list", "--dir", a, "--json"); err != nil {
					t.Fatalf("legacy baseline: %s %v", out, err)
				}
				stop := errors.New("revision boundary")
				if _, err := revisePolicyWithStep(a, revisionPolicyBytes(t), options, func(at string) error {
					if at == point {
						return stop
					}
					return nil
				}); !errors.Is(err, stop) {
					t.Fatal(err)
				}
				commonBefore := policyCommonBytes(t, a)
				for _, dir := range []string{a, b} {
					if dir == "" {
						continue
					}
					before := boardBytes(t, dir)
					for _, args := range [][]string{{"list", "--dir", dir, "--json"}, {"ready", "--dir", dir, "--json"}, {"create", "--dir", dir, "--title", "old-writer", "--json"}, {"reserve-ids", "--dir", dir, "--id", "TASK-99", "--json"}} {
						out, err := runLegacyPolicyCommand(binary, args...)
						var exit *exec.ExitError
						want := "activation"
						if shared {
							want = "object requires exact fields"
						}
						if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), want) {
							t.Fatalf("old writer did not hit revision barrier: %v %s %v", args, out, err)
						}
						if !reflect.DeepEqual(before, boardBytes(t, dir)) {
							t.Fatal("old writer changed board")
						}
					}
				}
				if !reflect.DeepEqual(commonBefore, policyCommonBytes(t, a)) {
					t.Fatal("old writer changed common state")
				}
			})
		}
	}
}
