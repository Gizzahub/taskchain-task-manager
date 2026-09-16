package taskstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBundleProcessHelper(t *testing.T) {
	if os.Getenv("TASKSTORE_BUNDLE_HELPER") != "1" {
		return
	}
	board, requestPath := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	raw, err := os.ReadFile(requestPath)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(2)
	}
	phase := os.Getenv("TASKSTORE_BUNDLE_CRASH")
	for attempt := 0; attempt < 100; attempt++ {
		result, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
			if phase != "" && phase == at {
				os.Exit(67)
			}
			return nil
		})
		if err == nil {
			fmt.Printf("%t:%s", result.Replayed, result.Tasks[0].ID)
			os.Exit(0)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "locked") {
			fmt.Fprint(os.Stderr, err)
			os.Exit(2)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Fprint(os.Stderr, "bounded lock retry exhausted")
	os.Exit(3)
}

func bundleChild(t *testing.T, board, requestPath, phase string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBundleProcessHelper$", "--", board, requestPath)
	cmd.Env = append(os.Environ(), "TASKSTORE_BUNDLE_HELPER=1", "TASKSTORE_BUNDLE_CRASH="+phase)
	return cmd
}

func TestBundleProcessCrashAndExplicitRecovery(t *testing.T) {
	phases := []string{"after-empty-journal", "after-local-protocol", "after-common-protocol", "after-pending-journal", "after-shared-reservation", "after-local-reservation", "after-card-0", "after-card-1", "after-batch", "after-completed-receipt", "after-common-clear"}
	for _, shared := range []bool{false, true} {
		for _, phase := range phases {
			if !shared && phase == "after-common-protocol" {
				continue
			}
			t.Run(fmt.Sprintf("shared-%t/%s", shared, phase), func(t *testing.T) {
				var board string
				if shared {
					_, board, _ = sharedFixture(t)
					if _, err := EnableShared(board, false); err != nil {
						t.Fatal(err)
					}
				} else {
					board = configuredFixture(t)
				}
				raw := publicationRequest(t, board)
				requestPath := filepath.Join(t.TempDir(), "request.json")
				if err := os.WriteFile(requestPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				commonLock := ""
				if shared {
					s, release, err := acquireShared(board, false)
					if err != nil {
						t.Fatal(err)
					}
					commonLock = filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey, ".task-manager.lock")
					if err := release(); err != nil {
						t.Fatal(err)
					}
				}
				cmd := bundleChild(t, board, requestPath, phase)
				output, err := cmd.CombinedOutput()
				if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 67 {
					t.Fatalf("crash not reached: %v %s", err, output)
				}
				_, err = List(board)
				assertLocked(t, err)
				// The fixture child is terminal. Remove only its known empty
				// lock directories; the product itself never steals these locks.
				if err := os.Remove(filepath.Join(board, ".task-manager.lock")); err != nil {
					t.Fatal(err)
				}
				if commonLock != "" {
					if err := os.Remove(commonLock); err != nil {
						t.Fatal(err)
					}
				}
				adoptionOnly := phase == "after-empty-journal" || phase == "after-local-protocol" || phase == "after-common-protocol"
				result, err := PublishBundle(board, raw, BundleOptions{Adopt: adoptionOnly, Resume: !adoptionOnly})
				if err != nil || result.Status != "completed" {
					t.Fatalf("recovery=%+v err=%v", result, err)
				}
				if _, err := List(board); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestBundleConcurrentProcesses(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared-%t", shared), func(t *testing.T) {
			var boards [2]string
			if shared {
				_, boards[0], boards[1] = sharedFixture(t)
				if _, err := EnableShared(boards[0], false); err != nil {
					t.Fatal(err)
				}
			} else {
				boards[0] = configuredFixture(t)
				boards[1] = boards[0]
			}
			var requests [2]string
			for i, board := range boards {
				raw := publicationRequest(t, board)
				requests[i] = filepath.Join(t.TempDir(), "request.json")
				if err := os.WriteFile(requests[i], raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			type outcome struct {
				text string
				err  error
			}
			results := make(chan outcome, 2)
			for i := 0; i < 2; i++ {
				cmd := bundleChild(t, boards[i], requests[i], "")
				go func(cmd *exec.Cmd) {
					raw, err := cmd.CombinedOutput()
					results <- outcome{strings.TrimSpace(string(raw)), err}
				}(cmd)
			}
			first, second := <-results, <-results
			if first.err != nil || second.err != nil {
				t.Fatalf("first=%+v second=%+v", first, second)
			}
			if shared {
				if first.text == second.text || !strings.HasPrefix(first.text, "false:") || !strings.HasPrefix(second.text, "false:") {
					t.Fatalf("shared allocations collide: %v %v", first, second)
				}
			} else if !((strings.HasPrefix(first.text, "false:") && strings.HasPrefix(second.text, "true:")) || (strings.HasPrefix(second.text, "false:") && strings.HasPrefix(first.text, "true:"))) {
				t.Fatalf("local replay incorrect: %v %v", first, second)
			}
			for _, board := range boards {
				if _, err := List(board); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
