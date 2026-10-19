package candidatediagnostic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func diagnosticGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git fixture: %s: %v", b, err)
	}
	return strings.TrimSpace(string(b))
}

func diagnosticWrite(t *testing.T, root, rel string, b []byte) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func diagnosticRepo(t *testing.T) (string, Input) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("safe candidate file reader is explicitly unavailable on this OS")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	diagnosticGit(t, root, "init", "-q")
	diagnosticWrite(t, root, "tasks/todo/a.md", []byte("synthetic card candidate\n"))
	diagnosticGit(t, root, "add", "tasks")
	diagnosticGit(t, root, "commit", "-qm", "fixture")
	b, err := boardpolicy.Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	file := diagnosticWrite(t, root, "candidate.json", b)
	return root, Input{Repo: root, Board: "tasks", CandidatePolicy: file, Source: "index"}
}

func diagnosticProjectionPolicy(t *testing.T) []byte {
	t.Helper()
	p, err := boardpolicy.NewV5(boardpolicy.Declaration{Modules: []string{}}, []boardpolicy.FileRoleV5{{Path: "README.md", Kind: boardpolicy.FileRoleProjection, Owner: "fixture", SourceRevision: "r1", Format: "live-card-paths-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCandidateDiagnosticSourcesAndExternalPolicy(t *testing.T) {
	root, in := diagnosticRepo(t)
	projection := "<!-- task-manager:projection-begin format=\"live-card-paths-v1\" owner=\"fixture\" source-revision=\"r1\" -->\n- todo/a.md\n<!-- task-manager:projection-end format=\"live-card-paths-v1\" owner=\"fixture\" source-revision=\"r1\" -->\n"
	diagnosticWrite(t, root, "tasks/README.md", []byte(projection))
	diagnosticGit(t, root, "add", "tasks")
	diagnosticGit(t, root, "commit", "-qm", "projection")
	diagnosticWrite(t, root, "candidate.json", diagnosticProjectionPolicy(t))
	diagnosticWrite(t, root, "tasks/README.md", []byte("invalid staged projection"))
	diagnosticGit(t, root, "add", "tasks/README.md")
	diagnosticWrite(t, root, "tasks/README.md", []byte(projection))
	diagnosticWrite(t, root, "tasks/todo/INDEX.md", []byte("local residue"))
	for _, tc := range []struct {
		source, path, severity string
		exit                   int
	}{
		{"ref", "", "", 0}, {"index", "README.md", "error", 1}, {"worktree", "todo/INDEX.md", "residue", 1},
	} {
		in.Source, in.Ref = tc.source, ""
		if tc.source == "ref" {
			in.Ref = "HEAD"
		}
		r := Assess(context.Background(), in)
		if r.ExitCode() != tc.exit || r.Error != nil || r.AuthorityChecked || r.Candidate == nil || r.Snapshot == nil {
			t.Fatalf("%s: %+v", tc.source, r)
		}
		if tc.exit != 0 && (len(r.Findings) != 1 || r.Findings[0].Path != tc.path || r.Findings[0].Severity != tc.severity) {
			t.Fatalf("mixed sources or classification: %s %+v", tc.source, r.Findings)
		}
		if (r.Snapshot.TreeOID != nil) != (tc.source == "ref") || (r.Snapshot.IndexDigest != nil) == (tc.source == "ref") {
			t.Fatalf("wrong provenance: %+v", r.Snapshot)
		}
	}
}

func TestCandidateDiagnosticFailurePriorityAndRedaction(t *testing.T) {
	root, in := diagnosticRepo(t)
	in.Repo = filepath.Join(root, "missing-repository-secret")
	diagnosticWrite(t, root, "candidate.json", []byte("secret-field-name: [broken"))
	r := Assess(context.Background(), in)
	if r.ExitCode() != 2 || r.Error.Code != "candidate-invalid" || r.Candidate != nil || r.Snapshot != nil || len(r.Findings) != 0 {
		t.Fatalf("candidate failure did not precede capture: %+v", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), root) || strings.Contains(string(b), "secret-field-name") {
		t.Fatalf("private input reflected: %s", b)
	}
	canonical, _ := boardpolicy.Default().Canonical()
	diagnosticWrite(t, root, "candidate.json", canonical)
	r = Assess(context.Background(), in)
	if r.Error.Code != "snapshot-unavailable" || r.Candidate == nil || r.Snapshot != nil {
		t.Fatalf("wrong completed sources: %+v", r)
	}
	in.CandidatePolicy = filepath.Join(root, "missing-policy-secret")
	r = Assess(context.Background(), in)
	if r.Error.Code != "candidate-read" || r.Candidate != nil {
		t.Fatalf("missing explicit policy fell back: %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = Assess(ctx, in)
	if r.Error.Code != "cancelled" || len(r.Findings) != 0 {
		t.Fatalf("cancellation lost: %+v", r)
	}
}

func TestCandidateDiagnosticCandidateHasNoFilenameExemption(t *testing.T) {
	root, in := diagnosticRepo(t)
	b, _ := boardpolicy.Default().Canonical()
	in.CandidatePolicy = diagnosticWrite(t, root, "tasks/candidate.json", b)
	diagnosticGit(t, root, "add", "tasks/candidate.json")
	r := Assess(context.Background(), in)
	if r.ExitCode() != 1 || len(r.Findings) != 1 || r.Findings[0].Path != "candidate.json" {
		t.Fatalf("candidate file was implicitly exempted: %+v", r)
	}
}

func diagnosticInventory(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, name)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(":%x", sha256.Sum256(b))
		}
		files[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCandidateDiagnosticDoesNotCheckOrMutateAuthority(t *testing.T) {
	for _, state := range []string{"absent", "normal", "corrupt", "pending"} {
		t.Run(state, func(t *testing.T) {
			root, in := diagnosticRepo(t)
			if state != "absent" {
				in.Board = "authority-board"
				board := filepath.Join(root, in.Board)
				if err := taskstore.Init(board); err != nil {
					t.Fatal(err)
				}
				b, _ := boardpolicy.Default().Canonical()
				if _, err := taskstore.ActivatePolicy(board, b, taskstore.PolicyActivationOptions{}); err != nil {
					t.Fatal(err)
				}
				if _, err := taskstore.ReadActivePolicy(board); err != nil {
					t.Fatal(err)
				}
				if state == "corrupt" {
					diagnosticWrite(t, root, in.Board+"/.task-manager-policy.json", []byte("corrupt authority"))
				}
				if state == "pending" {
					path := filepath.Join(board, ".task-manager-policy-activation.json")
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var activation map[string]any
					if err := json.Unmarshal(raw, &activation); err != nil {
						t.Fatal(err)
					}
					activation["phase"] = "pending"
					raw, err = json.Marshal(activation)
					if err != nil {
						t.Fatal(err)
					}
					diagnosticWrite(t, root, in.Board+"/.task-manager-policy-activation.json", raw)
				}
			}
			before := diagnosticInventory(t, root)
			in.Source = "worktree"
			r := Assess(context.Background(), in)
			if r.Error != nil || r.Candidate == nil || r.AuthorityChecked {
				t.Fatalf("authority affected candidate diagnostic: %+v", r)
			}
			if !reflect.DeepEqual(before, diagnosticInventory(t, root)) {
				t.Fatal("diagnostic changed board, index, refs, journal or file inventory")
			}
		})
	}
}
