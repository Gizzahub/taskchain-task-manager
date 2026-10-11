package taskruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnreadableAndMalformedDeclaration(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		write bool
		mode  os.FileMode
		want  string
	}{
		{name: "missing", want: "read .ce/task-runtime.yaml"},
		{name: "malformed", body: "schema-version: [\n", write: true, mode: 0o644, want: "decode .ce/task-runtime.yaml"},
		{name: "unreadable", body: "schema-version: 1\n", write: true, mode: 0, want: "read .ce/task-runtime.yaml"},
		{name: "legacy key", body: "worktree-root: /tmp/sample-repo\n", write: true, mode: 0o644, want: `use host-keyed "worktree-roots.<GIT_WORK_HOST>"`},
		{name: "schema range", body: "schema-version: 4\nrepository-id: sample-repo\nworktree-roots: {testhost: /tmp/sample-repo}\nintegration-provider: gz-git\n", write: true, mode: 0o644, want: "unsupported task runtime schema-version 4"},
		{name: "unknown schema 2 key", body: "schema-version: 2\nrepository-id: sample-repo\nworktree-roots: {testhost: /tmp/sample-repo}\nintegration-provider: gz-git\nintegration-network-policy: allow\ncard-dialect: {}\n", write: true, mode: 0o644, want: `unsupported task runtime config key "card-dialect" for schema-version 2`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.write {
				path := filepath.Join(root, ".ce")
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "task-runtime.yaml"), []byte(test.body), test.mode); err != nil {
					t.Fatal(err)
				}
			}
			_, err := (&Service{root: root}).config()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestSourceDeclarationShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		miss bool
		want string
		fail bool
	}{
		{name: "absent", miss: true, want: ""},
		{name: "scalar", body: "branch:\n  integrationBranch: master\n", want: "master"},
		{name: "sequence skips empty", body: "branch:\n  integrationBranch:\n    - \"\"\n    - trunk\n", want: "trunk"},
		{name: "empty scalar", body: "branch:\n  integrationBranch: \"\"\n", want: ""},
		{name: "mapping", body: "branch:\n  integrationBranch: {name: master}\n", want: ""},
		{name: "broken", body: "branch: [\n", fail: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if !test.miss {
				if err := os.WriteFile(filepath.Join(root, ".gz-git.yaml"), []byte(test.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := (&Service{root: root}).declaredSource()
			if test.fail {
				if err == nil || !strings.Contains(err.Error(), "cannot be parsed") {
					t.Fatalf("error %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("source %q err=%v, want %q", got, err, test.want)
			}
		})
	}
}
