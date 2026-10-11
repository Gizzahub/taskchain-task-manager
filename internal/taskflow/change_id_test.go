package taskflow

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestChangeIDFromCommitMessage(t *testing.T) {
	const id = "taskchain-gate/0123456789abcdef0123456789abcdef"
	tests := []struct {
		name, message, want string
		err                 string
	}{
		{"valid", "subject\n\nChange-Id: " + id + "\nModel: grok\n", id, ""},
		{"no trailer", "subject\n", "", "last nonempty trailer must be Model"},
		{"blank before model", "subject\nChange-Id: " + id + "\n\nModel: grok\n", id, ""},
		{"model missing", "subject\nChange-Id: " + id, "", "last nonempty trailer must be Model"},
		{"not adjacent", "subject\nChange-Id: " + id + "\nReviewed-by: x\nModel: grok", "", "Change-Id must immediately precede Model"},
		{"malformed", "subject\nChange-Id: nope\nModel: grok", "", `malformed Change-Id "nope"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChangeIDFromCommitMessage(tc.message)
			if got != tc.want {
				t.Fatalf("id=%q want %q", got, tc.want)
			}
			if tc.err == "" && err != nil {
				t.Fatal(err)
			}
			if tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
				t.Fatalf("err=%v want %q", err, tc.err)
			}
		})
	}
}

func TestBranchChangeIDSyntheticRepository(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "master")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "Test")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(dir+"/file", []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base\n")
	run("add", "file")
	run("commit", "-qm", "base")
	run("branch", "task")
	run("checkout", "-q", "task")
	write("change\n")
	run("add", "file")
	run("commit", "-qm", "subject\n\nChange-Id: taskchain-gate/0123456789abcdef0123456789abcdef\nModel: grok")
	if err := os.WriteFile(dir+"/.gz-git.yaml", []byte("branch:\n  integrationBranch: master\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("update-ref", "refs/remotes/origin/master", "master")
	got, err := BranchChangeID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "taskchain-gate/0123456789abcdef0123456789abcdef" {
		t.Fatalf("got %q", got)
	}
}

func TestChangeIDRangeContract(t *testing.T) {
	const id = "taskchain-gate/0123456789abcdef0123456789abcdef"
	valid := "subject\n\nChange-Id: " + id + "\nModel: synthetic\n"
	cases := []struct {
		name           string
		messages       []string
		want, fragment string
	}{
		{"skip prose", []string{"subject\nbody\n", valid}, id, ""},
		{"empty range", nil, "", "no Change-Id trailer"},
		{"duplicate values", []string{valid, valid}, "", "branch range declares 2"},
		{"malformed contextual", []string{"bad subject\nChange-Id: wrong\nModel: synthetic"}, "", `commit "bad subject": malformed`},
		{"empty subject", []string{"\nChange-Id: wrong\nModel: synthetic"}, "", `commit "(empty subject)": malformed`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChangeIDFromBranchRange(tc.messages)
			if got != tc.want || (tc.fragment == "" && err != nil) || (tc.fragment != "" && (err == nil || !strings.Contains(err.Error(), tc.fragment))) {
				t.Fatalf("got %q err %v", got, err)
			}
		})
	}
	for _, message := range []string{
		"subject\nChange-Id: " + id + "\nChange-Id: " + id + "\nModel: synthetic",
		"subject\nChange-Id: ce-gate/0123456789abcdef0123456789abcdef\nModel: synthetic",
		"subject\nChange-Id: taskchain-gate/ABCDEF0123456789abcdef0123456789ab\nModel: synthetic",
		"subject\nChange-Id: " + id + " \nModel: synthetic",
		"subject\nChange-Id: " + id + "\nModel:   ",
	} {
		if _, err := ChangeIDFromCommitMessage(message); err == nil {
			t.Fatalf("accepted %q", message)
		}
	}
	messages := splitGitLogMessages("subject\n\nbody\n\x00next\n\x00")
	if len(messages) != 2 || messages[0] != "subject\n\nbody\n" || messages[1] != "next\n" {
		t.Fatalf("raw messages %#v", messages)
	}
}

func TestBranchChangeIDSourceAndNearerRef(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "source")
	git("config", "user.name", "Synthetic")
	git("config", "user.email", "synthetic@example.invalid")
	commit := func(message string) string {
		t.Helper()
		git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", message)
		return git("rev-parse", "HEAD")
	}
	base := commit("base")
	git("checkout", "-qb", "task")
	near := commit("near base")
	head := commit("change\n\nChange-Id: taskchain-gate/0123456789abcdef0123456789abcdef\nModel: synthetic")
	git("update-ref", "refs/remotes/origin/source", base)
	git("update-ref", "refs/heads/source", near)
	if err := os.WriteFile(root+"/.gz-git.yaml", []byte("branch:\n  integrationBranch: [source]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	peer, err := sourceBranchRef(root, "source")
	if err != nil || peer != "refs/heads/source" {
		t.Fatalf("near local %q %v", peer, err)
	}
	git("update-ref", "refs/remotes/origin/source", near)
	git("update-ref", "refs/heads/source", base)
	peer, err = sourceBranchRef(root, "source")
	if err != nil || peer != "refs/remotes/origin/source" {
		t.Fatalf("near origin %q %v", peer, err)
	}
	git("update-ref", "refs/heads/source", near)
	peer, err = sourceBranchRef(root, "source")
	if err != nil || peer != "refs/remotes/origin/source" {
		t.Fatalf("equal %q %v", peer, err)
	}
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/source")
	if err := os.Remove(root + "/.gz-git.yaml"); err != nil {
		t.Fatal(err)
	}
	if got, err := BranchChangeID(root); err != nil || got != "taskchain-gate/0123456789abcdef0123456789abcdef" {
		t.Fatalf("fallback %q %v", got, err)
	}
	git("update-ref", "refs/remotes/origin/source", head)
	git("update-ref", "refs/heads/source", head)
	if _, err := BranchChangeID(root); err == nil || !strings.Contains(err.Error(), "range refs/remotes/origin/source..HEAD") {
		t.Fatalf("empty range context %v", err)
	}
	if err := os.WriteFile(root+"/.gz-git.yaml", []byte("branch: [broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := BranchChangeID(root); err == nil || !strings.Contains(err.Error(), "cannot be parsed") {
		t.Fatalf("invalid declaration %v", err)
	}
}

func TestBranchChangeIDHistoryBoundaries(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "base")
	git("config", "user.name", "Synthetic")
	git("config", "user.email", "synthetic@example.invalid")
	commit := func(msg string) string {
		t.Helper()
		git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", msg)
		return git("rev-parse", "HEAD")
	}
	base := commit("base")
	git("checkout", "-qb", "left")
	left := commit("left")
	git("checkout", "-qb", "right", base)
	right := commit("right")
	git("-c", "commit.gpgsign=false", "merge", "--no-ff", "-qm", "merge", "left")
	git("update-ref", "refs/remotes/origin/source", left)
	git("update-ref", "refs/heads/source", right)
	if _, err := sourceBranchRef(root, "source"); err == nil || !strings.Contains(err.Error(), "incomparable") {
		t.Fatalf("incomparable %v", err)
	}
	git("checkout", "--orphan", "unrelated")
	unrelated := commit("unrelated")
	git("checkout", "-q", "right")
	git("update-ref", "refs/remotes/origin/source", unrelated)
	if peer, err := sourceBranchRef(root, "source"); err != nil || peer != "refs/heads/source" {
		t.Fatalf("unrelated origin %q %v", peer, err)
	}
	git("update-ref", "refs/remotes/origin/source", right)
	git("update-ref", "refs/heads/source", unrelated)
	if peer, err := sourceBranchRef(root, "source"); err != nil || peer != "refs/remotes/origin/source" {
		t.Fatalf("unrelated local %q %v", peer, err)
	}
	git("update-ref", "refs/remotes/origin/source", unrelated)
	if _, err := sourceBranchRef(root, "source"); err == nil || !strings.Contains(err.Error(), "no common ancestor") {
		t.Fatalf("unrelated both %v", err)
	}
	if _, err := sourceBranchRef(root, "missing"); err == nil || !strings.Contains(err.Error(), "resolves neither") {
		t.Fatalf("missing refs %v", err)
	}
}
