package taskstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This opt-in check executes a separately built pre-bundle release, not a
// simulated reader. CI without that binary does not prove backwards fencing.
func TestBundleLegacyBinaryBarrier(t *testing.T) {
	t.Parallel()
	binary := os.Getenv("TASKCHAIN_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_LEGACY_BINARY to a pre-bundle executable")
	}
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "local-after-transition", true: "shared-markerless"}[shared], func(t *testing.T) {
			var owner, target string
			if shared {
				repo, board, _ := sharedFixture(t)
				owner = board
				if _, err := EnableShared(owner, false); err != nil {
					t.Fatal(err)
				}
				newRoot := filepath.Join(t.TempDir(), "markerless")
				sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
				target = filepath.Join(newRoot, "tasks")
			} else {
				owner = configuredFixture(t)
				target = owner
			}
			raw := publicationRequest(t, owner)
			result, err := PublishBundle(owner, raw, BundleOptions{Adopt: true})
			if err != nil {
				t.Fatal(err)
			}
			if !shared {
				id := result.Tasks[1].ID
				if _, err := Claim(owner, ClaimRequest{ID: id, Owner: "worker", Token: testToken}); err != nil {
					t.Fatal(err)
				}
				if _, err := Transition(owner, TransitionRequest{ID: id, Owner: "worker", Token: testToken, RequestID: strings.Repeat("c", 32), From: "todo", To: "doing"}); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, target)
			for _, args := range [][]string{
				{"list", "--dir", target, "--json"},
				{"ready", "--dir", target, "--json"},
				{"create", "--dir", target, "--title", "Old writer", "--json"},
				{"reserve-ids", "--dir", target, "--id", "TASK-99", "--json"},
			} {
				cmd := exec.Command(binary, args...)
				output, err := cmd.CombinedOutput()
				if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 {
					t.Fatalf("old command did not reject on data contract: %v %v %s", args, err, output)
				}
				if !strings.Contains(string(output), "journal") && !strings.Contains(string(output), "shared state") {
					t.Fatalf("unrelated legacy error: %s", output)
				}
				if !reflect.DeepEqual(before, boardBytes(t, target)) {
					t.Fatal("legacy operation modified adopted board")
				}
			}
		})
	}
}
