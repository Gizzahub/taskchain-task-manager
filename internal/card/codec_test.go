package card

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestParsePreservesUnknownMetadataAndBytes(t *testing.T) {
	raw := []byte("---\r\nid: TASK-7\r\ntitle: Known\r\nx-owner: private\r\ndepends-on: [TASK-1, TASK-2]\r\n---\r\n\r\n# Body title\r\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.Bytes(), raw) {
		t.Fatal("Bytes did not preserve source")
	}
	v := d.View()
	if v.Title != "Known" || v.Priority != "" || len(v.DependsOn) != 2 {
		t.Fatalf("view = %#v", v)
	}
	v.DependsOn[0] = "mutated"
	if d.View().DependsOn[0] != "TASK-1" {
		t.Fatal("View returned internal dependency slice")
	}
	copyBytes := d.Bytes()
	copyBytes[0] = 'x'
	if d.Bytes()[0] != '-' {
		t.Fatal("Bytes returned internal storage")
	}
}

func TestSnapshotZoneOverridesFrontmatter(t *testing.T) {
	d, err := Parse([]byte("---\nstatus: done\npriority: high\n---\n# Card\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Snapshot("tasks/todo/card.md").Status; got != "pending" {
		t.Fatalf("status = %q", got)
	}
	if got := d.Snapshot("tasks/backlog/card.md").Status; got != "done" {
		t.Fatalf("status = %q", got)
	}
}

func TestParseNeedsHumanBoolean(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "absent", raw: "---\nid: TASK-1\ntitle: Example\n---\n", want: false},
		{name: "true", raw: "---\nid: TASK-1\nneeds-human: true\n---\n", want: true},
		{name: "false", raw: "---\nid: TASK-1\nneeds-human: false\n---\n", want: false},
		{name: "merged true", raw: "---\nid: TASK-1\ndefaults: &defaults\n  needs-human: true\n<<: *defaults\n---\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := doc.NeedsHuman(); got != tc.want {
				t.Fatalf("NeedsHuman() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseRejectsMalformedNeedsHuman(t *testing.T) {
	for _, value := range []string{"\"true\"", "null", "1", "[true]"} {
		t.Run(value, func(t *testing.T) {
			_, err := Parse([]byte("---\nid: TASK-1\nneeds-human: " + value + "\n---\n"))
			if err == nil || !strings.Contains(err.Error(), "needs-human") {
				t.Fatalf("Parse() error = %v, want needs-human error", err)
			}
		})
	}
	_, err := Parse([]byte("---\nid: TASK-1\nneeds-human: false\nneeds-human: true\n---\n"))
	if err == nil {
		t.Fatal("duplicate needs-human accepted")
	}
	_, err = Parse([]byte("---\nid: TASK-1\ndefaults: &defaults\n  needs-human: maybe\n<<: *defaults\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "needs-human") {
		t.Fatalf("merged malformed needs-human error = %v", err)
	}
}

func TestParseExecutionMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "default", raw: "---\nid: TASK-1\n---\n", want: "implementation"},
		{name: "external", raw: "---\nid: TASK-1\nexecution-mode: external\n---\n", want: "external"},
		{name: "merged", raw: "---\nid: TASK-1\ndefaults: &defaults\n  execution-mode: decision\n<<: *defaults\n---\n", want: "decision"},
		{name: "direct matches merge", raw: "---\nid: TASK-1\ndefaults: &defaults\n  execution-mode: external\n<<: *defaults\nexecution-mode: external\n---\n", want: "external"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := doc.ExecutionMode(); got != tc.want {
				t.Fatalf("ExecutionMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRejectsInvalidExecutionMode(t *testing.T) {
	for _, value := range []string{"null", "true", "[external]", "manual", "'external '"} {
		t.Run(value, func(t *testing.T) {
			_, err := Parse([]byte("---\nid: TASK-1\nexecution-mode: " + value + "\n---\n"))
			if err == nil || !strings.Contains(err.Error(), "execution-mode") {
				t.Fatalf("Parse() error = %v, want execution-mode error", err)
			}
		})
	}
	_, err := Parse([]byte("---\nid: TASK-1\ndefault-a: &a\n  execution-mode: external\ndefault-b: &b\n  execution-mode: decision\n<<: [*a, *b]\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "conflicting effective") {
		t.Fatalf("conflicting merged execution-mode error = %v", err)
	}
	_, err = Parse([]byte("---\nid: TASK-1\ndefaults: &defaults\n  execution-mode: decision\n<<: *defaults\nexecution-mode: external\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "conflicting effective") {
		t.Fatalf("direct and merged execution-mode conflict error = %v", err)
	}
	_, err = Parse([]byte("---\nid: TASK-1\nexecution-mode: external\nexecution-mode: decision\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "execution-mode") {
		t.Fatalf("duplicate execution-mode error = %v", err)
	}
}

func TestParseAllowedPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    []string
		present bool
	}{
		{name: "absent", raw: "---\nid: TASK-1\n---\n", present: false},
		{name: "empty", raw: "---\nid: TASK-1\nallowed-paths: []\n---\n", want: []string{}, present: true},
		{name: "values", raw: "---\nid: TASK-1\nallowed-paths: [internal/card, docs]\n---\n", want: []string{"internal/card", "docs"}, present: true},
		{name: "preserves values", raw: "---\nid: TASK-1\nallowed-paths: [' internal/card ']\n---\n", want: []string{" internal/card "}, present: true},
		{name: "merged", raw: "---\nid: TASK-1\ndefaults: &defaults\n  allowed-paths: [internal/card]\n<<: *defaults\n---\n", want: []string{"internal/card"}, present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			got, present := doc.AllowedPaths()
			if present != tc.present || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("AllowedPaths() = %#v, %v; want %#v, %v", got, present, tc.want, tc.present)
			}
			if len(got) > 0 {
				got[0] = "mutated"
				if next, _ := doc.AllowedPaths(); next[0] == "mutated" {
					t.Fatal("AllowedPaths returned internal slice")
				}
			}
		})
	}
}

func TestParseRejectsInvalidAllowedPaths(t *testing.T) {
	for _, value := range []string{"null", "internal/card", "{}", "[internal/card, 1]", "['']", "['   ']"} {
		t.Run(value, func(t *testing.T) {
			_, err := Parse([]byte("---\nid: TASK-1\nallowed-paths: " + value + "\n---\n"))
			if err == nil || !strings.Contains(err.Error(), "allowed-paths") {
				t.Fatalf("Parse() error = %v, want allowed-paths error", err)
			}
		})
	}
	_, err := Parse([]byte("---\nid: TASK-1\ndefault-a: &a\n  allowed-paths: [internal/card]\ndefault-b: &b\n  allowed-paths: [docs]\n<<: [*a, *b]\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "conflicting effective") {
		t.Fatalf("conflicting merged allowed-paths error = %v", err)
	}
	_, err = Parse([]byte("---\nid: TASK-1\nallowed-paths: [internal/card]\nallowed-paths: [docs]\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "allowed-paths") {
		t.Fatalf("duplicate allowed-paths error = %v", err)
	}
	_, err = Parse([]byte("---\nid: TASK-1\n<<: [not-a-map]\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("malformed merge error = %v", err)
	}
}

func TestSnapshotRecognizesCEZoneAliases(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{"pending", "pending"}, {"wip", "in-progress"}, {"in_progress", "in-progress"},
		{"in-review", "review"}, {"suspended", "blocked"}, {"on_hold", "blocked"},
		{"completed", "done"}, {"finished", "done"},
	} {
		d, err := Parse([]byte("---\nstatus: open\n---\n# Card\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := d.Snapshot("tasks/" + tc.dir + "/card.md").Status; got != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestParseRejectsMalformedUnterminatedAndSequenceRoot(t *testing.T) {
	for name, raw := range map[string][]byte{
		"malformed":    []byte("---\na: [\n---\n# x\n"),
		"unterminated": []byte("---\ntitle: x\n# x\n"),
		"sequence":     []byte("---\n- one\n- two\n---\n# x\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(raw); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSetStatusCellSkipsFrontmatterAndNestedFences(t *testing.T) {
	raw := []byte("---\nexample: |\n  ```\n  **Status** | [ ] Pending\n  ```\n---\n````markdown\n| **Status** | [x] Done |\n```markdown\n| **Status** | [ ] Pending |\n```\n````\n| **Status** | [ ] Pending |\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("in-progress")
	if err != nil || !changed {
		t.Fatalf("patch = changed:%v err:%v", changed, err)
	}
	if bytes.Count(got, []byte("[~] In Progress")) != 1 {
		t.Fatalf("unexpected patch:\n%s", got)
	}
	if !bytes.Contains(got, []byte("[x] Done")) {
		t.Fatal("fenced sample changed")
	}
}

func TestSetStatusCellCRLFMissingAndInvalid(t *testing.T) {
	crlf := []byte("---\r\nstatus: pending\r\n---\r\n# Card\r\n| **Status** | [ ] Pending |\r\n")
	d, err := Parse(crlf)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("done")
	if err != nil || !changed || !bytes.Contains(got, []byte("\r\n| **Status** | [x] Done |\r\n")) {
		t.Fatalf("CRLF patch failed: changed=%v err=%v bytes=%q", changed, err, got)
	}
	if _, _, err := d.SetStatusCell("unknown"); err == nil {
		t.Fatal("invalid status accepted")
	}
	noCell, err := Parse([]byte("---\ntitle: x\n---\n# no cell\n"))
	if err != nil {
		t.Fatal(err)
	}
	unchanged, changed, err := noCell.SetStatusCell("done")
	if err != nil || changed || !bytes.Equal(unchanged, noCell.Bytes()) {
		t.Fatalf("missing cell: changed=%v err=%v", changed, err)
	}
}

func TestEscapedBackticksDoNotOpenFence(t *testing.T) {
	raw := []byte("---\ntitle: x\n---\n\\``` not a fence\n| **Status** | [ ] Pending |\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("done")
	if err != nil || !changed || !bytes.Contains(got, []byte("[x] Done")) {
		t.Fatalf("escaped fence: changed=%v err=%v bytes=%s", changed, err, got)
	}
}

func TestEscapedMarkerInsideFenceDoesNotExposeStatus(t *testing.T) {
	raw := []byte("---\ntitle: x\n---\n```\n\\```\n| **Status** | [ ] Pending |\n```\n| **Status** | [ ] Pending |\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("done")
	if err != nil || !changed {
		t.Fatalf("patch: changed=%v err=%v", changed, err)
	}
	if bytes.Count(got, []byte("[x] Done")) != 1 || !bytes.Contains(got, []byte("\\```\n| **Status** | [ ] Pending |")) {
		t.Fatalf("status inside active fence changed:\n%s", got)
	}
}

func TestSetStatusCellIgnoresProseAndInlineExamples(t *testing.T) {
	raw := []byte("---\ntitle: x\n---\nProse **Status** | [ ] Pending | and `| **Status** | [ ] Pending |`.\n| **Title** | **Status** | [ ] Pending |\n| **Status** | [ ] Pending |\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("done")
	if err != nil || !changed {
		t.Fatalf("patch: changed=%v err=%v", changed, err)
	}
	if !bytes.Contains(got, []byte("Prose **Status** | [ ] Pending |")) || !bytes.Contains(got, []byte("`| **Status** | [ ] Pending |`")) {
		t.Fatalf("prose/example changed:\n%s", got)
	}
	if !bytes.Contains(got, []byte("| **Title** | **Status** | [ ] Pending |")) || !bytes.Contains(got, []byte("| **Status** | [x] Done |")) {
		t.Fatalf("wrong table row changed:\n%s", got)
	}
}

func TestSetStatusCellPreservesReasonTail(t *testing.T) {
	raw := []byte("---\ntitle: x\n---\n| **Status** | [!] Blocked — waiting for owner |\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("doing")
	if err != nil || !changed {
		t.Fatalf("patch: changed=%v err=%v", changed, err)
	}
	want := []byte("---\ntitle: x\n---\n| **Status** | [~] In Progress — waiting for owner |\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("reason tail lost:\ngot  %q\nwant %q", got, want)
	}
}

func TestSetStatusCellIsIdempotent(t *testing.T) {
	raw := []byte("---\ntitle: x\n---\n| **Status** | [x] Done |\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("done")
	if err != nil || changed || !bytes.Equal(got, raw) {
		t.Fatalf("idempotent patch: changed=%v err=%v got=%q", changed, err, got)
	}
}

func TestBacktickInfoStringDoesNotOpenFence(t *testing.T) {
	raw := []byte("---\ntitle: x\n---\n```inline```\n| **Status** | [ ] Pending |\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("done")
	if err != nil || !changed || !bytes.Contains(got, []byte("[x] Done")) {
		t.Fatalf("info-string fence: changed=%v err=%v bytes=%s", changed, err, got)
	}
}

func TestSetStatusCellSupportsTildeFenceAndEOFFrontmatter(t *testing.T) {
	raw := []byte("---\ntitle: x\n---\n~~~markdown\n| **Status** | [ ] Pending |\n~~~\n| **Status** | [ ] Pending |")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetStatusCell("done")
	if err != nil || !changed {
		t.Fatalf("tilde patch: changed=%v err=%v", changed, err)
	}
	if bytes.Count(got, []byte("[x] Done")) != 1 || !bytes.Contains(got, []byte("~~~markdown\n| **Status** | [ ] Pending |")) {
		t.Fatalf("tilde fence was patched:\n%s", got)
	}
}
