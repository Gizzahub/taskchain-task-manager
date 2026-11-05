package taskflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

func TestManualPathZoneReplacesOnlyTheZoneSegment(t *testing.T) {
	cases := []struct {
		rel  string
		zone string
		ok   bool
		dest string
		to   string
	}{
		{rel: "todo/001-sign.md", zone: "todo", ok: true, to: "manual", dest: "manual/001-sign.md"},
		{rel: "doing/sub/001-sign.md", zone: "doing", ok: true, to: "manual", dest: "manual/sub/001-sign.md"},
		{rel: "wip/001-sign.md", zone: "doing", ok: true, to: "manual", dest: "manual/001-sign.md"},
		{rel: "manual/001-sign.md", zone: "manual", ok: true, to: "todo", dest: "todo/001-sign.md"},
		{rel: "backend/todo/auth/001-sign.md", zone: "todo", ok: true, to: "manual", dest: "backend/manual/auth/001-sign.md"},
		{rel: "backend/manual/auth/d.md", zone: "manual", ok: true, to: "todo", dest: "backend/todo/auth/d.md"},
		{rel: "backend/in-progress/auth/001-sign.md", zone: "doing", ok: true, to: "manual", dest: "backend/manual/auth/001-sign.md"},
		{rel: "backend/blocked/001-sign.md", zone: "blocked", ok: true, to: "manual", dest: "backend/manual/001-sign.md"},
		{rel: "review/001-sign.md", zone: "review", ok: true},
		{rel: "backend/review/auth/001-sign.md", zone: "review", ok: true},
		{rel: "notes/001-sign.md", ok: false},
		{rel: "backend/auth/001-sign.md", ok: false},
		{rel: "backend/auth/manual/001-sign.md", ok: false},
		{rel: "plan/manual/001-sign.md", ok: false},
		{rel: "_archive/manual/001-sign.md", ok: false},
	}
	for _, tc := range cases {
		_, zone, ok := manualPathZone(strings.Split(tc.rel, "/"))
		if ok != tc.ok || zone != tc.zone {
			t.Fatalf("%s zone %q ok %v, want %q ok %v", tc.rel, zone, ok, tc.zone, tc.ok)
		}
		if tc.dest == "" {
			continue
		}
		if got := destinationRel(tc.rel, tc.to); got != tc.dest {
			t.Fatalf("%s destination %q, want %q", tc.rel, got, tc.dest)
		}
	}
	if !manualZoneCard("tasks/manual/002-other.md") || !manualZoneCard("tasks/backend/manual/auth/d.md") {
		t.Fatal("manual zone card was not recognized")
	}
	if manualZoneCard("tasks/todo/001-sign.md") || manualZoneCard("tasks/backend/todo/auth/001-sign.md") || manualZoneCard("tasks/plan/manual/001-sign.md") {
		t.Fatal("non-manual path was treated as a manual card")
	}
}

func TestModuleZoneEntryExitKeepsTheModule(t *testing.T) {
	src := "tasks/backend/todo/auth/001-sign.md"
	watch := "---\nid: TASK-002\ntitle: watch\nstatus: todo\n---\n\nSee [sign](auth/001-sign.md)\n"
	root := initBoardForTest(t, map[string]string{
		src:                               section9Card(),
		"tasks/backend/todo/002-watch.md": watch,
	})
	digest, err := EnterManual(root, src, section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "tasks", "backend", "manual", "auth", "001-sign.md")
	body, err := os.ReadFile(moved)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "status: manual") {
		t.Fatalf("status not stamped: %s", body)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "backend", "todo", "auth", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("source card still present")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "auth", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("entry renamed the module")
	}
	watched, err := os.ReadFile(filepath.Join(root, "tasks", "backend", "todo", "002-watch.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(watched), "../manual/auth/001-sign.md") {
		t.Fatalf("module link was not retargeted: %s", watched)
	}
	raw, err := manualzone.Encode(doneReceipt(digest))
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, root, section9Receipt, raw)
	if err := ExitManual(root, "tasks/backend/manual/auth/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-module", testAuthority); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(root, "tasks", "backend", "todo", "auth", "001-sign.md")
	exited, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exited), "status: todo") {
		t.Fatalf("exit status: %s", exited)
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatal("exited card stayed in manual")
	}
}

func TestModuleAliasZoneEntersManual(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/backend/wip/auth/001-sign.md": section9Card()})
	if _, err := EnterManual(root, "tasks/backend/wip/auth/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "backend", "manual", "auth", "001-sign.md")); err != nil {
		t.Fatal("alias zone did not land under the module manual directory")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "auth", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("alias entry renamed the module")
	}
}

func TestModuleReissueStaysInModuleManual(t *testing.T) {
	useHumanWriter(t)
	root := initBoardForTest(t, map[string]string{"tasks/backend/todo/auth/001-sign.md": section9Card()})
	digest, err := EnterManual(root, "tasks/backend/todo/auth/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "tasks", "backend", "manual", "auth", "001-sign.md")
	body, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(body) + "\nedited outside the writer\n"
	if err := os.WriteFile(cardPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := CanonicalReviewDigest(edited)
	if err != nil {
		t.Fatal(err)
	}
	req := manualzone.ReissueRequest{
		ActorKind: "human", Reason: "operator edited the card", RequestID: "reissue-module",
		PreviousGeneration: manualGeneration, PreviousDigest: digest, CurrentDigest: current,
	}
	if _, err := ReissueManual(root, "tasks/backend/manual/auth/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority, req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cardPath); err != nil {
		t.Fatal("reissue moved the module card")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("reissue renamed the module")
	}
}

func TestModuleManualLinkRefusesTheWholeEntry(t *testing.T) {
	other := "---\nid: TASK-002\ntitle: other\nstatus: manual\n---\n\nSee [sign](../../todo/auth/001-sign.md)\n"
	root := initBoardForTest(t, map[string]string{
		"tasks/backend/todo/auth/001-sign.md":    section9Card(),
		"tasks/backend/manual/auth/002-other.md": other,
	})
	before := readTree(t, root)
	_, err := EnterManual(root, "tasks/backend/todo/auth/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err == nil || !strings.Contains(err.Error(), "another manual card") {
		t.Fatalf("module manual link err = %v", err)
	}
	if readTree(t, root) != before {
		t.Fatal("refused module manual link changed the tree")
	}
}

func TestManualEntryRefusesAZoneThatIsNotTodoDoingOrBlocked(t *testing.T) {
	for _, rel := range []string{
		"tasks/backend/auth/manual/001-sign.md",
		"tasks/plan/manual/001-sign.md",
		"tasks/backend/notes/001-sign.md",
		"tasks/backend/review/001-sign.md",
	} {
		root := initBoardForTest(t, map[string]string{rel: section9Card()})
		before := readTree(t, root)
		if _, err := EnterManual(root, rel, section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
			t.Fatalf("%s entered manual", rel)
		}
		if readTree(t, root) != before {
			t.Fatalf("%s changed the tree", rel)
		}
	}
}

func TestModuleEntryResumeUsesTheSourceZone(t *testing.T) {
	linked := strings.Replace(section9Card(), "body\n", "body\n\nSee [other](002-other.md)\n", 1)
	stamped, ok := stampFrontmatterContent([]byte(linked), [][2]string{{"status", "manual"}})
	if !ok {
		t.Fatal("fixture stamp failed")
	}
	root := initBoardForTest(t, map[string]string{
		"tasks/backend/manual/auth/001-sign.md": string(stamped),
	})
	writeEntryBaseline(t, root, linked)
	if _, err := EnterManual(root, "tasks/backend/todo/auth/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "tasks", "backend", "manual", "auth", "001-sign.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "../../todo/auth/002-other.md") {
		t.Fatalf("module resume did not retarget from the source zone: %s", body)
	}
	entry := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	if _, err := os.Stat(entry); err != nil {
		t.Fatal("module resume did not publish the entry")
	}
}
