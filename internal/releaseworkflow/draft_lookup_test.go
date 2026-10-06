package releaseworkflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Draft tag lookup returns 404. The workflow pages the releases list, keeps
// exactly one v0.1.0 tag, and fetches that release by its numeric id.
func TestReleaseV010DraftLookup(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required to run the draft lookup script")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-v0.1.0.yml")
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	script, extractErr := extractLookup(workflow)

	t.Run("workflow", func(t *testing.T) {
		if extractErr != nil {
			t.Fatal(extractErr)
		}
		if strings.Contains(workflow, "releases/tags") {
			t.Fatal("workflow still requests a release by tag")
		}
		assertOrder(t, workflow,
			"lookup_draft_release() {",
			"\n          lookup_draft_release\n",
			"gh release download v0.1.0",
			"gh attestation verify",
			"assets no",
			"gh release upload v0.1.0",
			"assets yes",
			"-F draft=false",
		)
		publish := strings.Index(workflow, "-F draft=false")
		if !strings.Contains(workflow[publish:], "verify_file ") {
			t.Fatal("published binary is not attested again")
		}
		for _, piece := range []string{
			`docs/release-v0.1.0.json`,
			`dd3ec0a0848586bcbdecaf598bde2b6d38b979c4`,
			`a0ee91bd7169bc228262b829f08ff1fcf00fe910`,
			`c01ce7aad3638ddc87acda2c9bd52e6d72a52f2a40d932d7e238f246f91e939e`,
			`8451634`,
			`SHA256SUMS provenance.json taskchain-task-manager-darwin-arm64`,
			`https://github.com/Gizzahub/taskchain-task-manager/verified-release/v1`,
			`gh api "repos/${REPO}/releases/${RELEASE_ID}"`,
			`repos/${REPO}/releases?per_page=100&page=${page}`,
			`repos/${REPO}/releases/${match_id}`,
			`workflow_dispatch:`,
			`refs/heads/master`,
		} {
			if !strings.Contains(workflow, piece) {
				t.Fatalf("workflow lost %q", piece)
			}
		}
		syntax := exec.Command(bash, "-n")
		syntax.Stdin = strings.NewReader("set -euo pipefail\n" + script + "\n")
		if out, err := syntax.CombinedOutput(); err != nil {
			t.Fatalf("lookup script: %v\n%s", err, out)
		}
	})
	if extractErr != nil {
		return
	}

	repo := "Gizzahub/taskchain-task-manager"
	draft := gotRelease{listRelease: listRelease{ID: 77, Draft: true, Tag: "v0.1.0"}, FetchedBy: "numeric-id"}
	page2 := gotRelease{listRelease: listRelease{ID: 9001, Draft: true, Tag: "v0.1.0"}, FetchedBy: "numeric-id"}

	t.Run("valid draft", func(t *testing.T) {
		pages := [][]listRelease{{
			{ID: 1, Draft: true, Tag: "v0.1.0-rc"},
			{ID: 77, Draft: true, Tag: "v0.1.0"},
			{ID: 2, Draft: false, Tag: "v0.2.0"},
		}}
		got := runLookup(t, bash, script, repo, pages, map[int]gotRelease{77: draft})
		if got.exit != 0 {
			t.Fatalf("exit %d stderr %s", got.exit, got.stderr)
		}
		if got.body.FetchedBy != "numeric-id" || got.body.ID != 77 || !got.body.Draft || got.body.Tag != "v0.1.0" {
			t.Fatalf("release = %+v", got.body)
		}
		assertCalls(t, got.calls,
			listCall(repo, 1),
			idCall(repo, 77),
		)
	})

	t.Run("missing", func(t *testing.T) {
		pages := [][]listRelease{{
			{ID: 3, Draft: true, Tag: "v0.1.0-rc"},
			{ID: 4, Draft: true, Tag: "V0.1.0"},
			{ID: 5, Draft: false, Tag: "v0.1.0 "},
		}}
		got := runLookup(t, bash, script, repo, pages, nil)
		if got.exit == 0 || !strings.Contains(got.stderr, "missing") {
			t.Fatalf("exit %d stderr %s", got.exit, got.stderr)
		}
		assertNoReleaseFile(t, got.work)
		assertCalls(t, got.calls, listCall(repo, 1))
	})

	t.Run("duplicate", func(t *testing.T) {
		page := otherReleases(100, 1)
		page[20] = listRelease{ID: 11, Draft: true, Tag: "v0.1.0"}
		pages := [][]listRelease{
			page,
			{{ID: 12, Draft: false, Tag: "v0.1.0"}},
		}
		got := runLookup(t, bash, script, repo, pages, nil)
		if got.exit == 0 || !strings.Contains(got.stderr, "duplicate") {
			t.Fatalf("exit %d stderr %s", got.exit, got.stderr)
		}
		assertNoReleaseFile(t, got.work)
		assertCalls(t, got.calls, listCall(repo, 1), listCall(repo, 2))
	})

	t.Run("public", func(t *testing.T) {
		pages := [][]listRelease{{
			{ID: 8, Draft: false, Tag: "v0.1.0"},
		}}
		got := runLookup(t, bash, script, repo, pages, nil)
		if got.exit == 0 || !strings.Contains(got.stderr, "already public") {
			t.Fatalf("exit %d stderr %s", got.exit, got.stderr)
		}
		assertNoReleaseFile(t, got.work)
		assertCalls(t, got.calls, listCall(repo, 1))
	})

	t.Run("pagination", func(t *testing.T) {
		page := otherReleases(100, 1000)
		page[0].Tag = "v0.1.0-rc"
		pages := [][]listRelease{
			page,
			{{ID: 9001, Draft: true, Tag: "v0.1.0"}},
		}
		got := runLookup(t, bash, script, repo, pages, map[int]gotRelease{9001: page2})
		if got.exit != 0 {
			t.Fatalf("exit %d stderr %s", got.exit, got.stderr)
		}
		if got.body.FetchedBy != "numeric-id" || got.body.ID != 9001 || !got.body.Draft || got.body.Tag != "v0.1.0" {
			t.Fatalf("release = %+v", got.body)
		}
		assertCalls(t, got.calls, listCall(repo, 1), listCall(repo, 2), idCall(repo, 9001))
	})
}

type listRelease struct {
	ID    int    `json:"id"`
	Draft bool   `json:"draft"`
	Tag   string `json:"tag_name"`
}

type gotRelease struct {
	listRelease
	FetchedBy string `json:"fetchedBy"`
}

type lookupResult struct {
	body   gotRelease
	calls  []string
	stderr string
	work   string
	exit   int
}

func extractLookup(workflow string) (string, error) {
	lines := strings.Split(workflow, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "lookup_draft_release() {" {
			continue
		}
		if start >= 0 {
			return "", fmt.Errorf("lookup_draft_release is defined more than once")
		}
		start = i
	}
	if start < 0 {
		return "", fmt.Errorf("workflow does not define lookup_draft_release")
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", fmt.Errorf("lookup_draft_release does not close")
	}
	if end+1 >= len(lines) || strings.TrimSpace(lines[end+1]) != "lookup_draft_release" {
		return "", fmt.Errorf("workflow does not call lookup_draft_release")
	}
	return strings.Join(lines[start:end+1], "\n") + "\n", nil
}

func assertOrder(t *testing.T, file string, parts ...string) {
	t.Helper()
	prev := -1
	for _, part := range parts {
		i := strings.Index(file, part)
		if i < 0 {
			t.Fatalf("workflow missing %q", part)
		}
		if i < prev {
			t.Fatalf("%q is out of order", part)
		}
		prev = i
	}
}

func otherReleases(n, start int) []listRelease {
	rows := make([]listRelease, n)
	for i := range rows {
		rows[i] = listRelease{ID: start + i, Draft: true, Tag: fmt.Sprintf("other-%d", start+i)}
	}
	return rows
}

func listCall(repo string, page int) string {
	return fmt.Sprintf("repos/%s/releases?per_page=100&page=%d", repo, page)
}

func idCall(repo string, id int) string {
	return fmt.Sprintf("repos/%s/releases/%d", repo, id)
}

func runLookup(t *testing.T, bash, script, repo string, pages [][]listRelease, fetched map[int]gotRelease) lookupResult {
	t.Helper()
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	work := filepath.Join(root, "work")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{fixture, work, bin} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i, page := range pages {
		writeJSON(t, filepath.Join(fixture, fmt.Sprintf("page-%d.json", i+1)), page)
	}
	for id, release := range fetched {
		writeJSON(t, filepath.Join(fixture, fmt.Sprintf("release-%d.json", id)), release)
	}
	fake := "#!/bin/bash\n" + fakeGH
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := "set -euo pipefail\n" + script + "lookup_draft_release\n"
	cmd := exec.Command(bash)
	cmd.Stdin = strings.NewReader(runner)
	cmd.Env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"REPO=" + repo,
		"FIXTURE_DIR=" + fixture,
		"work=" + work,
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := lookupResult{stderr: stderr.String(), work: work, calls: readCalls(t, filepath.Join(fixture, "calls.txt"))}
	if err != nil {
		var exitErr *exec.ExitError
		if !os.IsTimeout(err) && !errorsAsExit(err, &exitErr) {
			t.Fatalf("lookup: %v stderr %s", err, result.stderr)
		}
		result.exit = exitErr.ExitCode()
		return result
	}
	raw, err := os.ReadFile(filepath.Join(work, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &result.body); err != nil {
		t.Fatal(err)
	}
	return result
}

func errorsAsExit(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = exitErr
	return true
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCalls(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func assertCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls\n got %v\nwant %v", got, want)
	}
}

func assertNoReleaseFile(t *testing.T, work string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(work, "release.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("release.json after rejection: %v", err)
	}
}

const fakeGH = `
set -euo pipefail
if [ "${1:-}" != "api" ] || [ "$#" -ne 2 ]; then
  printf '%s\n' "unexpected gh invocation: $*" >&2
  exit 2
fi
url=$2
printf '%s\n' "$url" >> "${FIXTURE_DIR}/calls.txt"
repo=${REPO:?}
list_prefix="repos/${repo}/releases?per_page=100&page="
id_prefix="repos/${repo}/releases/"
case "$url" in
  "${list_prefix}"*)
    page=${url#"$list_prefix"}
    case "$page" in
      ''|*[!0-9]*)
        printf '%s\n' "unexpected page: ${url}" >&2
        exit 2
        ;;
    esac
    file="${FIXTURE_DIR}/page-${page}.json"
    if [ -f "$file" ]; then
      cat "$file"
    else
      printf '%s\n' '[]'
    fi
    ;;
  "${id_prefix}"*)
    id=${url#"$id_prefix"}
    case "$id" in
      ''|*[!0-9]*)
        printf '%s\n' "unexpected release id lookup: ${url}" >&2
        exit 2
        ;;
    esac
    file="${FIXTURE_DIR}/release-${id}.json"
    if [ ! -f "$file" ]; then
      printf '%s\n' "release ${id} was not prepared" >&2
      exit 1
    fi
    cat "$file"
    ;;
  *)
    printf '%s\n' "unexpected endpoint: ${url}" >&2
    exit 2
    ;;
esac
`
