package taskruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func canonicalTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func canonicalTestRepo(t *testing.T, separate bool) (string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(base, "primary with spaces")
	args := []string{"init", "--quiet", "--initial-branch=synthetic"}
	if separate {
		args = append(args, "--separate-git-dir="+filepath.Join(base, "separate metadata"))
	}
	canonicalTestGit(t, base, append(args, primary)...)
	canonicalTestGit(t, primary, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "synthetic fixture")
	linked := filepath.Join(base, "linked with spaces")
	canonicalTestGit(t, primary, "worktree", "add", "--quiet", "--detach", linked)
	return primary, linked
}

type canonicalQueryRunner struct {
	mutate func(CommandRequest, *CommandResult)
}

func (r canonicalQueryRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	res, err := NewExecutor().Run(ctx, req)
	if err == nil && r.mutate != nil {
		r.mutate(req, &res)
	}
	return res, err
}

func TestCanonicalRepositoryPrimaryAndLinked(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprint("separate=", separate), func(t *testing.T) {
			primary, linked := canonicalTestRepo(t, separate)
			for _, root := range []string{primary, linked} {
				s := NewService(root, NewExecutor())
				got, err := s.canonicalRepository(context.Background())
				if err != nil || got.primary.root != primary || got.caller.root != root || got.configPath() != filepath.Join(primary, ConfigFile) {
					t.Fatalf("root %s: %+v %v", root, got, err)
				}
				if _, err := os.Stat(filepath.Join(got.primary.commonDir, "ce")); !os.IsNotExist(err) {
					t.Fatalf("resolver wrote lifecycle state or cannot prove absence: %v", err)
				}
			}
			// Inventory order and branch naming are not primary authority.
			runner := canonicalQueryRunner{mutate: func(req CommandRequest, res *CommandResult) {
				if len(req.Args) > 0 && req.Args[0] == "worktree" {
					records := strings.Split(strings.TrimSuffix(res.Stdout, "\x00\x00"), "\x00\x00")
					res.Stdout = records[1] + "\x00\x00" + records[0] + "\x00\x00"
				}
			}}
			got, err := NewService(linked, runner).canonicalRepository(context.Background())
			if err != nil || got.primary.root != primary {
				t.Fatalf("reversed inventory: %+v %v", got, err)
			}
		})
	}
}

func TestCanonicalRepositoryRejectsShadow(t *testing.T) {
	for _, kind := range []string{"copy", "dangling-symlink", "other-worktree"} {
		t.Run(kind, func(t *testing.T) {
			primary, linked := canonicalTestRepo(t, false)
			if err := os.MkdirAll(filepath.Join(primary, ".ce"), 0o700); err != nil {
				t.Fatal(err)
			}
			data := []byte("opaque config bytes, deliberately not parsed\n")
			if err := os.WriteFile(filepath.Join(primary, ConfigFile), data, 0o600); err != nil {
				t.Fatal(err)
			}
			shadowRoot := linked
			if kind == "other-worktree" {
				shadowRoot = filepath.Join(filepath.Dir(primary), "another linked")
				canonicalTestGit(t, primary, "worktree", "add", "--quiet", "--detach", shadowRoot)
			}
			if err := os.MkdirAll(filepath.Join(shadowRoot, ".ce"), 0o700); err != nil {
				t.Fatal(err)
			}
			shadow := filepath.Join(shadowRoot, ConfigFile)
			var err error
			if kind == "dangling-symlink" {
				err = os.Symlink("missing-target", shadow)
			} else {
				err = os.WriteFile(shadow, data, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewService(linked, NewExecutor()).canonicalRepository(context.Background()); err == nil {
				t.Fatal("accepted a non-primary config shadow")
			}
			if _, err := os.Lstat(shadow); err != nil {
				t.Fatal("resolver changed shadow", err)
			}
			after, err := os.ReadFile(filepath.Join(primary, ConfigFile))
			if err != nil || string(after) != string(data) {
				t.Fatal("resolver changed primary config", err)
			}
		})
	}
}

func TestCanonicalRepositoryRejectsUnavailable(t *testing.T) {
	primary, linked := canonicalTestRepo(t, false)
	t.Run("bare", func(t *testing.T) {
		bare := filepath.Join(t.TempDir(), "bare")
		canonicalTestGit(t, primary, "clone", "--quiet", "--bare", primary, bare)
		if _, err := NewService(bare, NewExecutor()).canonicalRepository(context.Background()); err == nil {
			t.Fatal("accepted bare repository")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := NewService(primary, NewExecutor()).canonicalRepository(ctx); err == nil {
			t.Fatal("accepted failed Git query")
		}
	})
	for _, kind := range []string{"empty", "unknown-field", "no-primary", "duplicate", "missing-primary", "foreign-common", "query-failure"} {
		t.Run(kind, func(t *testing.T) {
			foreign, _ := canonicalTestRepo(t, false)
			runner := canonicalQueryRunner{mutate: func(req CommandRequest, res *CommandResult) {
				if len(req.Args) == 0 || req.Args[0] != "worktree" {
					return
				}
				records := strings.Split(strings.TrimSuffix(res.Stdout, "\x00\x00"), "\x00\x00")
				switch kind {
				case "empty":
					res.Stdout = ""
				case "unknown-field":
					res.Stdout = strings.Replace(res.Stdout, "\x00\x00", "\x00unknown-field\x00\x00", 1)
				case "no-primary":
					res.Stdout = records[1] + "\x00\x00"
				case "duplicate":
					res.Stdout += records[0] + "\x00\x00"
				case "missing-primary":
					res.Stdout = strings.Replace(res.Stdout, primary, primary+"-missing", 1)
				case "foreign-common":
					res.Stdout = strings.Replace(res.Stdout, primary, foreign, 1)
				case "query-failure":
					res.ExitCode = 1
				}
			}}
			if _, err := NewService(linked, runner).canonicalRepository(context.Background()); err == nil {
				t.Fatal("accepted unavailable/inconsistent inventory")
			}
		})
	}
	t.Run("redirected-env", func(t *testing.T) {
		t.Setenv("GIT_DIR", filepath.Join(primary, ".git"))
		if _, err := NewService(linked, NewExecutor()).canonicalRepository(context.Background()); err == nil {
			t.Fatal("accepted redirected Git environment")
		}
	})
}

func TestCanonicalRepositoryRevalidation(t *testing.T) {
	for _, kind := range []string{"common-replaced", "topology-changed", "shadow-added", "primary-replaced", "during-query"} {
		t.Run(kind, func(t *testing.T) {
			primary, linked := canonicalTestRepo(t, false)
			s := NewService(linked, NewExecutor())
			before, err := s.canonicalRepository(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "common-replaced":
				old := before.primary.commonDir + "-retained"
				if err := os.Rename(before.primary.commonDir, old); err != nil {
					t.Fatal(err)
				}
				canonicalTestGit(t, primary, "init", "--quiet")
			case "primary-replaced":
				if err := os.Rename(primary, primary+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(primary, 0o700); err != nil {
					t.Fatal(err)
				}
			case "topology-changed":
				canonicalTestGit(t, primary, "worktree", "add", "--quiet", "--detach", filepath.Join(filepath.Dir(primary), "new-linked"))
			case "shadow-added":
				if err := os.MkdirAll(filepath.Join(linked, ".ce"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(linked, ConfigFile), []byte("shadow"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "during-query":
				count := 0
				s.runner = canonicalQueryRunner{mutate: func(req CommandRequest, res *CommandResult) {
					if len(req.Args) > 0 && req.Args[0] == "worktree" {
						count++
						if count == 2 {
							res.Stdout = strings.Replace(res.Stdout, "detached\x00", "locked synthetic\x00detached\x00", 1)
						}
					}
				}}
				if _, err := s.canonicalRepository(context.Background()); err == nil {
					t.Fatal("accepted inventory drift during observation")
				}
				return
			}
			if err := before.revalidate(context.Background(), s); err == nil {
				t.Fatal("accepted stale canonical identity")
			}
		})
	}
}
