package taskstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestArchiveAdmissionBindsSourceAndExplicitMetadata(t *testing.T) {
	t.Parallel()
	cfg, err := archivepolicy.ParseConfig([]byte(archiveCompletionRulesFixture))
	if err != nil {
		t.Fatal(err)
	}
	p := boardpolicy.Default()
	raw := []byte("---\nid: TASK-1\nstatus: pending\nreview-result: waived\nreview-proof: reason for waiver\n---\n")
	got, err := observeArchiveAdmission(raw, "TASK-1", "done/TASK-1.md", p, cfg, nil)
	if err != nil || !got.Allowed || !got.CompletionEligible {
		t.Fatalf("workflow authority lost: %+v %v", got, err)
	}
	before := string(raw)
	got, err = observeArchiveAdmission(raw, "TASK-1", "todo/TASK-1.md", p, cfg, nil)
	if err != nil || got.Allowed || string(raw) != before {
		t.Fatalf("todo admission or byte mutation: %+v %v", got, err)
	}
	for _, tc := range []struct {
		id, path string
		raw      []byte
	}{
		{"TASK-01", "done/TASK-1.md", raw},
		{"TASK-1", "_archive/done/TASK-1.md", raw},
		{"TASK-1", "done/TASK-1.md", []byte(strings.Replace(string(raw), "status: pending", "status: superseded", 1))},
	} {
		if _, err := observeArchiveAdmission(tc.raw, tc.id, tc.path, p, cfg, nil); err == nil {
			t.Fatal("accepted mismatched or terminal source")
		}
	}
}

func TestArchiveAdmissionResolvesDeclaredChildrenAndPromotions(t *testing.T) {
	t.Parallel()
	cfg, err := archivepolicy.ParseConfig([]byte(archiveCompletionRulesFixture))
	if err != nil {
		t.Fatal(err)
	}
	p, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, source, fields string }{
		{"PLAN-1", "backend/plan/auth/PLAN-1.md", "child-ids: [TASK-2, TASK-3]\n"},
		{"ISSUE-1", "backend/issue/auth/ISSUE-1.md", "disposition: fixed\npromoted: [TASK-2, TASK-3]\n"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			raw := []byte("---\nid: " + tc.id + "\n" + tc.fields + "---\n")
			calls := []string{}
			got, err := observeArchiveAdmission(raw, tc.id, tc.source, p, cfg, func(id string) (bool, error) { calls = append(calls, id); return true, nil })
			if err != nil || !got.Allowed || got.CompletionEligible || strings.Join(calls, ",") != "TASK-2,TASK-3" {
				t.Fatalf("wrong census: %+v %v %v", got, calls, err)
			}
			got, err = observeArchiveAdmission(raw, tc.id, tc.source, p, cfg, func(id string) (bool, error) { return id == "TASK-2", nil })
			if err != nil || got.Allowed {
				t.Fatalf("unfinished reference allowed: %+v %v", got, err)
			}
			want := errors.New("invalid completion receipt")
			if _, err := observeArchiveAdmission(raw, tc.id, tc.source, p, cfg, func(string) (bool, error) { return false, want }); !errors.Is(err, want) {
				t.Fatalf("lost resolver error %v", err)
			}
		})
	}
}
