package taskflow

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestCardsOnlyDiagnosticsShareFindingsAndPreserveInput(t *testing.T) {
	snapshot, err := boardfiles.NewCardsOnlySnapshot(boardfiles.SnapshotWorktree, []boardfiles.SnapshotEntry{
		{Path: "todo/INDEX.md", Tracked: true}, {Path: "todo/image.png"},
		{Path: "evidence/proof.md", Tracked: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := boardfiles.AssessCardsOnlySnapshot(snapshot, boardpolicy.Default())
	if err != nil {
		t.Fatal(err)
	}
	before := append([]boardfiles.SnapshotFinding(nil), findings...)
	lint := CardsOnlyLintDiagnostics("work", findings)
	if lint.Clean || len(lint.Structure) != 2 || len(lint.Warnings) != 1 || lint.Structure[0].Path != "work/evidence" {
		t.Fatalf("lint=%+v", lint)
	}
	all := CardsOnlyValidationWarnings("work", findings, nil)
	if len(all) != len(findings) {
		t.Fatalf("validation lost a finding: %+v", all)
	}
	for _, result := range all {
		if !result.Valid() || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0].Message, "contract:") || !strings.Contains(result.Warnings[0].Message, "action:") {
			t.Fatalf("invalid advisory result: %+v", result)
		}
	}
	named := CardsOnlyValidationWarnings("work", findings, []string{"evidence/proof.md", "evidence/other.md"})
	if len(named) != 1 || named[0].Path != "work/evidence" {
		t.Fatalf("pruned ancestor lost or duplicated: %+v", named)
	}
	for _, selected := range [][]string{{}, {"evidence-other/proof.md"}, {"todo/absent.md"}} {
		if got := CardsOnlyValidationWarnings("work", findings, selected); len(got) != 0 {
			t.Fatalf("unrelated path matched ancestor: %+v", got)
		}
	}
	if !reflect.DeepEqual(before, findings) {
		t.Fatal("diagnostic mapping changed classifier result")
	}
	residue := CardsOnlyLintDiagnostics("tasks", []boardfiles.SnapshotFinding{{Path: "tmp.txt", Severity: boardfiles.SnapshotIssueResidue}})
	if !residue.Clean || len(residue.Warnings) != 1 {
		t.Fatal("residue changed lint verdict")
	}
	unknown := CardsOnlyLintDiagnostics("tasks", []boardfiles.SnapshotFinding{{Path: "tmp.txt"}})
	if unknown.Clean {
		t.Fatal("unknown severity silently became advisory")
	}
}
