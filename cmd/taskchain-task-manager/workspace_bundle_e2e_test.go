package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

const (
	workspaceBundleBuildTimeout = 30 * time.Second
	workspaceBundleGitTimeout   = 10 * time.Second
	workspaceBundleCLITimeout   = 15 * time.Second
)

// TestWorkspaceBundleRunsFromUnrelatedCWD proves that the checked-in skill is
// self-contained: a caller can copy the complete bundle and invoke the CLI by
// its PATH name without treating this checkout as the working directory.
func TestWorkspaceBundleRunsFromUnrelatedCWD(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sourceBundle := filepath.Join(repoRoot, "skills", "task-manager-workspace")
	sourceDigest := workspaceBundleDigest(t, sourceBundle)

	root := t.TempDir()
	copiedBundle := filepath.Join(root, "task-manager-workspace")
	copyWorkspaceBundle(t, sourceBundle, copiedBundle)
	copiedBundleDigest := workspaceBundleDigest(t, copiedBundle)
	bundleWorkspace := filepath.Join(copiedBundle, "assets", "workspace")
	workspaceRoot := filepath.Join(root, "workspace-fixture")
	copyWorkspaceBundle(t, bundleWorkspace, workspaceRoot)
	contextGolden := mustReadWorkspaceBundleFile(t, filepath.Join(bundleWorkspace, "expected-workspace-context.json"))
	queryGolden := mustReadWorkspaceBundleFile(t, filepath.Join(bundleWorkspace, "expected-query-beta.json"))
	manifest := filepath.Join(workspaceRoot, "workspace.json")
	manifestBefore := mustReadWorkspaceBundleFile(t, manifest)

	for _, name := range []string{"alpha", "beta"} {
		workspaceGitInit(t, filepath.Join(workspaceRoot, "repos", name))
	}
	fixtureBefore := workspaceBundleDigest(t, workspaceRoot)

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "taskchain-task-manager")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), workspaceBundleBuildTimeout)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-mod=readonly", "-o", binary, "./cmd/taskchain-task-manager")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			t.Fatalf("build CLI timed out after %s", workspaceBundleBuildTimeout)
		}
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	unrelated := filepath.Join(root, "unrelated")
	if err := os.Mkdir(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	code, stdout, stderr := runWorkspaceBundleCLI(t, unrelated,
		"workspace-context", "--manifest", manifest,
		"--card-id", "TASK-1", "--card-id", "TASK-2", "--card-id", "TASK-3", "--json")
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("workspace-context: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertWorkspaceBundleGolden(t, "workspace-context", stdout, contextGolden)

	code, stdout, stderr = runWorkspaceBundleCLI(t, unrelated,
		"query-workspace", "--manifest", manifest, "--repository", "beta", "--card-id", "TASK-1", "--json")
	if code != 0 || len(stderr) != 0 {
		t.Fatalf("scoped query: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertWorkspaceBundleGolden(t, "scoped query", stdout, queryGolden)

	for _, args := range [][]string{
		{"query-workspace", "--manifest", manifest, "--card-id", "TASK-1", "--json"},
		{"query-workspace", "--manifest", manifest, "--card-id", "TASK-3", "--json"},
	} {
		code, stdout, stderr = runWorkspaceBundleCLI(t, unrelated, args...)
		if code == 0 || len(stdout) != 0 || len(stderr) == 0 {
			t.Fatalf("query %v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}

	if after := mustReadWorkspaceBundleFile(t, manifest); !bytes.Equal(after, manifestBefore) {
		t.Fatal("workspace manifest changed")
	}
	if after := workspaceBundleDigest(t, workspaceRoot); after != fixtureBefore {
		t.Fatal("workspace fixture cards or assets changed")
	}
	assertNoWorkspaceLock(t, workspaceRoot)
	if after := workspaceBundleDigest(t, sourceBundle); after != sourceDigest {
		t.Fatal("source skill bundle changed")
	}
	if after := workspaceBundleDigest(t, copiedBundle); after != copiedBundleDigest {
		t.Fatal("copied installed skill bundle changed")
	}
}

func runWorkspaceBundleCLI(t *testing.T, cwd string, args ...string) (int, []byte, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), workspaceBundleCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "taskchain-task-manager", args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stdout.Bytes(), stderr.Bytes()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("CLI %v timed out after %s", args, workspaceBundleCLITimeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdout.Bytes(), stderr.Bytes()
	}
	t.Fatalf("run CLI %v: %v", args, err)
	return 0, nil, nil
}

func assertWorkspaceBundleGolden(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("%s output differs from bundle golden:\ngot:  %s\nwant: %s", name, got, want)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(got, &envelope); err != nil {
		t.Fatalf("%s output is not JSON: %v", name, err)
	}
	if string(envelope["outputVersion"]) != "1" {
		t.Fatalf("%s outputVersion = %s, want 1", name, envelope["outputVersion"])
	}
}

func workspaceGitInit(t *testing.T, repository string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), workspaceBundleGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "init", "-q", repository)
	if output, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("git init %s timed out after %s", repository, workspaceBundleGitTimeout)
		}
		t.Fatalf("git init %s: %v\n%s", repository, err, output)
	}
}

func copyWorkspaceBundle(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported bundle entry %s", path)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
}

func workspaceBundleDigest(t *testing.T, root string) [sha256.Size]byte {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported bundle entry %s", path)
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		contents := mustReadWorkspaceBundleFile(t, path)
		writeWorkspaceDigest(t, hash, []byte(filepath.ToSlash(relative)))
		writeWorkspaceDigest(t, hash, []byte{0})
		writeWorkspaceDigest(t, hash, contents)
		writeWorkspaceDigest(t, hash, []byte{0})
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func writeWorkspaceDigest(t *testing.T, hash io.Writer, data []byte) {
	t.Helper()
	if _, err := hash.Write(data); err != nil {
		t.Fatal(err)
	}
}

func mustReadWorkspaceBundleFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func assertNoWorkspaceLock(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == ".task-manager.lock" {
			return fmt.Errorf("workspace command left lock %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
