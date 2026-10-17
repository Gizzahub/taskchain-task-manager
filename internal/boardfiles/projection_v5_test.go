package boardfiles

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func v5Projection() boardpolicy.FileRoleV5 {
	return boardpolicy.FileRoleV5{Path: "README.md", Kind: boardpolicy.FileRoleProjection,
		Owner: "indexer", SourceRevision: "r1", Format: "live-card-paths-v1"}
}

func v5Policy(t *testing.T, roles ...boardpolicy.FileRoleV5) boardpolicy.Policy {
	t.Helper()
	if roles == nil {
		roles = []boardpolicy.FileRoleV5{}
	}
	p, err := boardpolicy.NewV5(boardpolicy.Declaration{Modules: []string{"backend"}}, roles)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func projectionFile(role boardpolicy.FileRoleV5, paths ...string) string {
	begin, end := projectionMarkers(role)
	sort.Strings(paths)
	body := ""
	for _, p := range paths {
		body += "- " + p + "\n"
	}
	return "Manual prefix\r\n\n" + string(begin) + body + string(end) + "\x00Manual suffix\r\n"
}

func TestV5GoldenProjectionAndContentAreReadOnly(t *testing.T) {
	fixture := filepath.Join("..", "boardpolicy", "testdata", "file-roles-v5")
	raw, err := os.ReadFile(filepath.Join(fixture, "policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := boardpolicy.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string][]byte{}
	for from, to := range map[string]string{"projection.md": "README.md", "AGENTS.md": "AGENTS.md", "TEMPLATE.md": "TEMPLATE.md"} {
		data, err := os.ReadFile(filepath.Join(fixture, from))
		if err != nil {
			t.Fatal(err)
		}
		files[to] = data
		write(t, root, "tasks/"+to, string(data))
	}
	write(t, root, "tasks/todo/001-example.md", "not parsed as a card by the scanner")
	result, err := Scan(root, "tasks", p)
	if err != nil || len(result.Findings) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, "tasks", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("scanner changed %s: %v", name, err)
		}
	}
}

func TestV5ProjectionInventoryAndMembershipDrift(t *testing.T) {
	for _, change := range []string{"none", "add", "delete", "move"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			paths := []string{"todo/001-root.md", "backend/todo/auth/002-module.md", "backend/plan/design/003-plan.md", "done/004-done.md", "issue/005-issue.md", "backend/todo/auth/006-a- b.md"}
			for _, p := range paths {
				write(t, root, "tasks/"+p, "body, title, status and ID are deliberately not parsed")
			}
			for _, p := range []string{"archive/2026-10/007-old.md", "_archive/done/008-old.md", "evidence/proof.md", "backend/todo/evidence/proof.md", ".ce/data/card.md", ".task-manager-context/intents/x/1.json"} {
				write(t, root, "tasks/"+p, "excluded")
			}
			role := v5Projection()
			write(t, root, "tasks/README.md", projectionFile(role, paths...))
			switch change {
			case "add":
				write(t, root, "tasks/backend/todo/new/009-added.md", "card")
			case "delete":
				if err := os.Remove(filepath.Join(root, "tasks", filepath.FromSlash(paths[1]))); err != nil {
					t.Fatal(err)
				}
			case "move":
				if err := os.Rename(filepath.Join(root, "tasks", filepath.FromSlash(paths[1])), filepath.Join(root, "tasks/backend/todo/auth/010-moved.md")); err != nil {
					t.Fatal(err)
				}
			}
			result, err := Scan(root, "tasks", v5Policy(t, role))
			if err != nil {
				t.Fatal(err)
			}
			if change == "none" {
				if len(result.Findings) != 0 {
					t.Fatalf("inventory findings: %+v", result.Findings)
				}
			} else if len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Detail, "inventory") {
				t.Fatalf("missing drift: %+v", result.Findings)
			}
		})
	}
}

func TestV5StructuralFailureDoesNotCompareIncompleteInventory(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tasks/unknown/hidden.md", "card")
	write(t, root, "tasks/README.md", "intentionally invalid projection")
	result, err := Scan(root, "tasks", v5Policy(t, v5Projection()))
	if err != nil || len(result.Findings) != 1 || result.Findings[0].Path != "unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestV5ProjectionMarkerBoundariesAndEmptyInventory(t *testing.T) {
	role := v5Projection()
	begin, end := projectionMarkers(role)
	good := string(begin) + string(end)
	for name, data := range map[string]string{
		"missing": "manual", "duplicate begin": string(begin) + good,
		"duplicate end": good + string(end), "reversed": string(end) + string(begin),
		"wrong owner":  strings.ReplaceAll(good, "indexer", "other"),
		"wrong format": strings.ReplaceAll(good, "live-card-paths-v1", "custom"),
		"CRLF":         strings.ReplaceAll(good, "\n", "\r\n"),
		"inline":       "manual" + good, "missing LF": strings.TrimSuffix(good, "\n"),
		"nested foreign": string(begin) + projectionPrefix + "oops\n" + string(end),
		"manual marker":  good + projectionPrefix + "oops",
		"nonempty":       string(begin) + "- todo/001.md\n" + string(end),
	} {
		t.Run(name, func(t *testing.T) {
			if issue := projectionIssue([]byte(data), role, nil); issue == "" {
				t.Fatal("accepted malformed projection")
			}
		})
	}
	for _, data := range []string{good, projectionFile(role)} {
		if issue := projectionIssue([]byte(data), role, nil); issue != "" {
			t.Fatalf("empty inventory: %s", issue)
		}
	}
}

func TestV5MarkerEscapesIdentityAndPathsRoundTrip(t *testing.T) {
	role := v5Projection()
	role.Owner = "team\"<>&/한글\u2028\u2029"
	role.SourceRevision = "r\\1"
	begin, _ := projectionMarkers(role)
	want := `owner="team\"\u003c\u003e\u0026/한글\u2028\u2029" source-revision="r\\1"`
	if !strings.Contains(string(begin), want) {
		t.Fatalf("escaping changed: %s", begin)
	}
	for _, rel := range []string{"todo/a- b.md", "backend/plan/한글/001.md"} {
		if issue := projectionPathIssue(rel); issue != "" {
			t.Fatalf("valid path %q: %s", rel, issue)
		}
	}
	for _, rel := range []string{"todo/a\n.md", "todo/a\x00.md", "todo/a\x7f.md", "todo/\xff.md", "todo/a\\b.md", "/todo/a.md", "todo//a.md", "todo/./a.md", "todo/../a.md", "todo/a.md ", "todo/a-->.md", "todo/<!-- task-manager:projection-a.md"} {
		if projectionPathIssue(rel) == "" {
			t.Fatalf("accepted ambiguous path %q", rel)
		}
	}
}

func TestV5ContentDigestUsesExactBytesAndRoleSizeLimits(t *testing.T) {
	for _, body := range []string{"guide\n", "guide\r\n", "\xffguide\n"} {
		root := t.TempDir()
		role := boardpolicy.FileRoleV5{Path: "AGENTS.md", Kind: boardpolicy.FileRoleGuidance,
			Owner: "guide", SourceRevision: "r1", ContentSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("guide\n")))}
		write(t, root, "tasks/AGENTS.md", body)
		result, err := Scan(root, "tasks", v5Policy(t, role))
		if err != nil || (len(result.Findings) == 0) != (body == "guide\n") {
			t.Fatalf("body=%q result=%+v err=%v", body, result, err)
		}
	}
	for _, kind := range []boardpolicy.FileRoleKind{boardpolicy.FileRoleGuidance, boardpolicy.FileRoleProjection} {
		for _, extra := range []int{0, 1} {
			root := t.TempDir()
			role, limit := v5Projection(), 1<<20
			body := projectionFile(role)
			if kind == boardpolicy.FileRoleGuidance {
				role.Path, role.Kind, role.Format = "AGENTS.md", kind, ""
				limit, body = 64<<10, ""
			}
			body += strings.Repeat("x", limit+extra-len(body))
			if kind == boardpolicy.FileRoleGuidance {
				role.ContentSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
			}
			write(t, root, "tasks/"+role.Path, body)
			result, err := Scan(root, "tasks", v5Policy(t, role))
			if err != nil || (extra == 0 && len(result.Findings) != 0) ||
				(extra == 1 && (len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Detail, "exceed"))) {
				t.Fatalf("kind=%s extra=%d result=%+v err=%v", kind, extra, result, err)
			}
		}
	}
}

func TestV5InventorySortsUTF8PathsAndRejectsControlNames(t *testing.T) {
	root, role := t.TempDir(), v5Projection()
	for _, name := range []string{"한글.md", "é.md", "a- b.md", "Z.md"} {
		write(t, root, "tasks/todo/"+name, "card")
	}
	begin, end := projectionMarkers(role)
	write(t, root, "tasks/README.md", string(begin)+"- todo/Z.md\n- todo/a- b.md\n- todo/é.md\n- todo/한글.md\n"+string(end))
	p := v5Policy(t, role)
	result, err := Scan(root, "tasks", p)
	if err != nil || len(result.Findings) != 0 {
		t.Fatalf("sorted result=%+v err=%v", result, err)
	}
	write(t, root, "tasks/todo/invalid\nname.md", "card")
	result, err = Scan(root, "tasks", p)
	if err != nil || len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Detail, "round-trip") {
		t.Fatalf("control path result=%+v err=%v", result, err)
	}
}

func TestV5PinnedRootAndMissingDeclaredFiles(t *testing.T) {
	root, role := t.TempDir(), v5Projection()
	write(t, root, "tasks/README.md", projectionFile(role))
	r, err := os.OpenRoot(filepath.Join(root, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := os.Rename(filepath.Join(root, "tasks"), filepath.Join(root, "retained")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "tasks/README.md", "replacement")
	result, err := ScanRoot(r, v5Policy(t, role))
	if err != nil || len(result.Findings) != 0 {
		t.Fatalf("pinned result=%+v err=%v", result, err)
	}
	if err := os.Remove(filepath.Join(root, "tasks/README.md")); err != nil {
		t.Fatal(err)
	}
	result, err = Scan(root, "tasks", v5Policy(t, role))
	if err != nil || len(result.Findings) != 1 || result.Findings[0].Detail != "declared file role is missing" {
		t.Fatalf("missing result=%+v err=%v", result, err)
	}
}
