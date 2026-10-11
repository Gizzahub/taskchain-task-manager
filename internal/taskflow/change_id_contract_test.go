package taskflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sampleChangeID = "taskchain-gate/0123456789abcdef0123456789abcdef"

func TestChangeIDCommitMessageRules(t *testing.T) {
	good := "subject\n\nChange-Id: " + sampleChangeID + "\nModel: test-1\n"
	id, err := ChangeIDFromCommitMessage(good)
	if err != nil || id != sampleChangeID {
		t.Fatalf("good: %v %q", err, id)
	}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"model not last", "Change-Id: " + sampleChangeID + "\nModel: test-1\nSigned-off-by: A\n", "last nonempty trailer must be Model"},
		{"not adjacent", "Change-Id: " + sampleChangeID + "\nNote: mid\nModel: test-1\n", "Change-Id must immediately precede Model"},
		{"count", "Change-Id: " + sampleChangeID + "\nChange-Id: " + sampleChangeID + "\nModel: test-1\n", "need exactly one Change-Id trailer, found 2"},
		{"malformed", "Change-Id: taskchain-gate/ZZ\nModel: test-1\n", `malformed Change-Id "taskchain-gate/ZZ"`},
		{"uppercase", "Change-Id: taskchain-gate/0123456789ABCDEF0123456789ABCDEF\nModel: test-1\n", "malformed Change-Id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ChangeIDFromCommitMessage(tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if commitSubject("\n\n") != "(empty subject)" {
		t.Fatal("empty subject label")
	}
}

func TestChangeIDBranchRange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitContract(t, root, nil, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, ".gz-git.yaml"), []byte("branch:\n  integrationBranch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitContract(t, root, nil, "add", "-A")
	gitContract(t, root, nil, "commit", "-qm", "source")

	_, err := BranchChangeID(ctx, root)
	if err == nil || !strings.Contains(err.Error(), "no Change-Id trailer in the branch range: main..HEAD") || !strings.Contains(err.Error(), "(range main..HEAD)") {
		t.Fatalf("empty range: %v", err)
	}

	gitContract(t, root, nil, "checkout", "-b", "work")
	gitContract(t, root, nil, "commit", "-qm", "no trailer", "--allow-empty")
	commitMsg(t, root, "land the change\n\nChange-Id: "+sampleChangeID+"\nModel: test-1\n", nil)
	id, err := BranchChangeID(ctx, root)
	if err != nil || id != sampleChangeID {
		t.Fatalf("success: %v %q", err, id)
	}

	other := "taskchain-gate/abcdef0123456789abcdef0123456789"
	commitMsg(t, root, "second\n\nChange-Id: "+other+"\nModel: test-1\n", nil)
	_, err = BranchChangeID(ctx, root)
	if err == nil || !strings.Contains(err.Error(), "branch range declares 2 Change-Id trailers") || !strings.Contains(err.Error(), "(range main..HEAD)") {
		t.Fatalf("two trailers: %v", err)
	}
}

func TestChangeIDSourceUnresolvedHasNoRangeSuffix(t *testing.T) {
	root := t.TempDir()
	gitContract(t, root, nil, "init", "-b", "main")
	gitContract(t, root, nil, "commit", "-qm", "source", "--allow-empty")
	_, err := BranchChangeID(context.Background(), root)
	if err == nil || err.Error() != "source branch is not declared and origin/HEAD is unresolved" || strings.Contains(err.Error(), "(range") {
		t.Fatalf("got %v", err)
	}
}

func TestChangeIDFromBranchRangeCommitPrefix(t *testing.T) {
	root := t.TempDir()
	gitContract(t, root, nil, "init", "-b", "main")
	gitContract(t, root, nil, "commit", "-qm", "base", "--allow-empty")
	gitContract(t, root, nil, "checkout", "-b", "work")
	commitMsg(t, root, "broken trailer\n\nChange-Id: taskchain-gate/ZZ\nModel: test-1\n", nil)
	_, err := ChangeIDFromBranchRange(context.Background(), root, "refs/heads/main")
	if err == nil || !strings.Contains(err.Error(), `commit "broken trailer": malformed Change-Id`) {
		t.Fatalf("got %v", err)
	}
}

func commitMsg(t *testing.T, dir, message string, env []string) {
	t.Helper()
	path := filepath.Join(dir, ".commit-msg")
	if err := os.WriteFile(path, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}
	gitContract(t, dir, env, "add", "-A")
	gitContract(t, dir, env, "commit", "-q", "-F", path, "--allow-empty")
}

func gitContract(t *testing.T, dir string, extra []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Tester",
		"GIT_AUTHOR_EMAIL=tester@example.com",
		"GIT_COMMITTER_NAME=Tester",
		"GIT_COMMITTER_EMAIL=tester@example.com",
	)
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
