package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModuleBundleIntermediateSharedBarrier(t *testing.T) {
	binary := os.Getenv("TASKCHAIN_MODULE_BUNDLE_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_MODULE_BUNDLE_LEGACY_BINARY to module-aware pre-destination executable")
	}
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
			for _, dir := range []string{board, other} {
				if out, err := runLegacyPolicyCommand(binary, "list", "--dir", dir, "--json"); err != nil {
					t.Fatalf("old module baseline: %s %v", out, err)
				}
			}
			s, release, err := acquireSharedForPolicy(board)
			if err != nil {
				t.Fatal(err)
			}
			commonPath := filepath.Join(s.root.Name(), sharedStateFile)
			if err := release(); err != nil {
				t.Fatal(err)
			}
			stop := errors.New("requested bundle boundary")
			if _, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("boundary not reached: %v", err)
			}
			before, beforeOther := boardBytes(t, board), boardBytes(t, other)
			common, err := os.ReadFile(commonPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{board, other} {
				for _, args := range [][]string{{"create", "--dir", dir, "--title", "old", "--json"}, {"reserve-ids", "--dir", dir, "--id", "TASK-999", "--json"}} {
					out, err := runLegacyPolicyCommand(binary, args...)
					if err == nil || !strings.Contains(string(out), "shared namespace has a pending bundle; recover from its original board") {
						t.Fatalf("old writer not fenced: %v %s %v", args, out, err)
					}
					after, err := os.ReadFile(commonPath)
					if err != nil || !bytes.Equal(common, after) || !reflect.DeepEqual(before, boardBytes(t, board)) || !reflect.DeepEqual(beforeOther, boardBytes(t, other)) {
						t.Fatalf("old writer changed shared state: %v", err)
					}
				}
			}
			if _, err := PublishBundle(board, raw, BundleOptions{Resume: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
