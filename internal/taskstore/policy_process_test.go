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

func TestPolicyProcessHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("TASKSTORE_POLICY_HELPER") != "1" {
		return
	}
	board := os.Args[len(os.Args)-1]
	phase := os.Getenv("TASKSTORE_POLICY_CRASH")
	for attempt := 0; attempt < 100; attempt++ {
		result, err := activatePolicyWithStep(board, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
			if phase != "" && phase == at {
				os.Exit(67)
			}
			return nil
		})
		if err == nil {
			fmt.Printf("%t:%s", result.Replayed, result.AuthorityID)
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

func policyChild(t *testing.T, board, phase string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPolicyProcessHelper$", "--", board)
	cmd.Env = append(os.Environ(), "TASKSTORE_POLICY_HELPER=1", "TASKSTORE_POLICY_CRASH="+phase)
	return cmd
}

func TestPolicyProcessCrashAndExplicitRecovery(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		phases := []string{"after-local-pending", "after-policy", "after-policy-ids", "after-policy-journal", "after-local-completed"}
		if shared {
			for i := range phases {
				phases[i] = "board-0/" + phases[i]
			}
			phases = append(phases, "after-common-policy-pending", "after-policy-board-0", "after-policy-board-1", "after-common-policy-active")
		}
		for _, phase := range phases {
			t.Run(fmt.Sprintf("shared-%t/%s", shared, phase), func(t *testing.T) {
				boards := []string{claimBoard(t)}
				commonLock := ""
				if shared {
					_, a, b := sharedFixture(t)
					boards = []string{a, b}
					if _, err := EnableShared(a, false); err != nil {
						t.Fatal(err)
					}
					s, release, err := acquireShared(a, false)
					if err != nil {
						t.Fatal(err)
					}
					commonLock = filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey, ".task-manager.lock")
					if err := release(); err != nil {
						t.Fatal(err)
					}
				}
				cmd := policyChild(t, boards[0], phase)
				output, err := cmd.CombinedOutput()
				if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 67 {
					t.Fatalf("crash not reached: %v %s", err, output)
				}
				for _, board := range boards {
					_, err := List(board)
					assertLocked(t, err)
					// The child is verified terminal. Only remove its known empty
					// fixture lock directories; normal recovery never steals locks.
					if err := os.Remove(filepath.Join(board, ".task-manager.lock")); err != nil {
						t.Fatal(err)
					}
				}
				if commonLock != "" {
					if err := os.Remove(commonLock); err != nil {
						t.Fatal(err)
					}
				}
				authority := savedPolicyAuthority(t, boards[0], shared)
				completed := phase == "after-common-policy-active" || (!shared && phase == "after-local-completed")
				if !completed {
					if _, err := ActivatePolicy(boards[0], defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}); err == nil || !strings.Contains(err.Error(), "resume") {
						t.Fatalf("implicit recovery: %v", err)
					}
				}
				result, err := ActivatePolicy(boards[0], defaultPolicyBytes(t), PolicyActivationOptions{Resume: true, AllWorktrees: true})
				if err != nil || result.Status != "completed" || result.AuthorityID != authority {
					t.Fatalf("resume=%+v %v", result, err)
				}
				for _, board := range boards {
					if _, err := List(board); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func savedPolicyAuthority(t *testing.T, board string, shared bool) string {
	t.Helper()
	if shared {
		s, release, err := acquireSharedForPolicy(board)
		if err != nil {
			t.Fatal(err)
		}
		id := s.state.Policy.AuthorityID
		if err := release(); err != nil {
			t.Fatal(err)
		}
		return id
	}
	r, err := openBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	state, err := loadPolicyActivation(r)
	if err != nil {
		t.Fatal(err)
	}
	return state.AuthorityID
}

func TestPolicyJoinProcessCrashWithIDReplacement(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-common-policy-pending", "board-0/after-policy-ids", "board-0/after-policy-journal"} {
		t.Run(phase, func(t *testing.T) {
			repo, a, _ := sharedFixture(t)
			if _, err := EnableShared(a, false); err != nil {
				t.Fatal(err)
			}
			initial, err := ActivatePolicy(a, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true})
			if err != nil {
				t.Fatal(err)
			}
			newRoot := filepath.Join(t.TempDir(), "join")
			sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
			board := filepath.Join(newRoot, "tasks")
			s, release, err := acquireSharedForPolicy(board)
			if err != nil {
				t.Fatal(err)
			}
			commonLock := filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey, ".task-manager.lock")
			if err := release(); err != nil {
				t.Fatal(err)
			}
			cmd := policyChild(t, board, phase)
			output, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 67 {
				t.Fatalf("join crash not reached: %v %s", err, output)
			}
			_, err = List(a)
			assertLocked(t, err)
			for _, path := range []string{filepath.Join(board, ".task-manager.lock"), commonLock} {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			r, err := openBoard(board)
			if err != nil {
				t.Fatal(err)
			}
			ledger, err := loadIDs(r)
			r.Close()
			wantVersion := 3
			if phase == "after-common-policy-pending" {
				wantVersion = 2
			}
			if err != nil || ledger.SchemaVersion != wantVersion {
				t.Fatalf("actual ID publication=%+v %v", ledger, err)
			}
			result, err := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{Resume: true, AllWorktrees: true})
			if err != nil || result.AuthorityID != initial.AuthorityID || result.Status != "completed" {
				t.Fatalf("join resume=%+v %v", result, err)
			}
			if _, err := List(board); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPolicyConcurrentProcesses(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared-%t", shared), func(t *testing.T) {
			board := claimBoard(t)
			boards := [2]string{board, board}
			if shared {
				_, boards[0], boards[1] = sharedFixture(t)
				if _, err := EnableShared(boards[0], false); err != nil {
					t.Fatal(err)
				}
			}
			type outcome struct {
				text string
				err  error
			}
			results := make(chan outcome, 2)
			for _, dir := range boards {
				cmd := policyChild(t, dir, "")
				go func() {
					raw, err := cmd.CombinedOutput()
					results <- outcome{strings.TrimSpace(string(raw)), err}
				}()
			}
			a, b := <-results, <-results
			if a.err != nil || b.err != nil {
				t.Fatalf("first=%+v second=%+v", a, b)
			}
			ap, bp := strings.Split(a.text, ":"), strings.Split(b.text, ":")
			if len(ap) != 2 || len(bp) != 2 || ap[1] != bp[1] || len(ap[1]) != 32 || !((ap[0] == "true" && bp[0] == "false") || (ap[0] == "false" && bp[0] == "true")) {
				t.Fatalf("authority or replay mismatch: %q %q", a.text, b.text)
			}
			for _, dir := range boards {
				if _, err := List(dir); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
