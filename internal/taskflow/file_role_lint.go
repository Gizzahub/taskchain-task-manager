package taskflow

import (
	"fmt"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// LintActive applies the file-role scanner only when the board's active,
// authority-bound policy opted into schema 4. Older policy schemas retain the
// exact legacy lint path and output.
func LintActive(root string) (*LintReport, error) {
	policy, err := ActiveBoardPolicy(root)
	if err != nil {
		return nil, err
	}
	return LintWithPolicy(root, policy)
}

// LintWithPolicy runs the normal lint classification and, for an opted-in
// policy, adds the common board-file findings to the existing Structure
// envelope. It is exported for policy-bound writer and CLI callers; callers
// must obtain policy through ActiveBoardPolicy rather than parsing a policy
// file directly.
func LintWithPolicy(root string, policy boardpolicy.Policy) (*LintReport, error) {
	if !policy.FileRolesEnabled() {
		return Lint(root), nil
	}
	tasksDir, err := ResolveTasksDir()
	if err != nil {
		return nil, err
	}
	// Scan before opening candidate cards so a card-shaped symlink is reported
	// as a structure violation rather than followed by the Markdown reader.
	scan, err := boardfiles.Scan(root, tasksDir, policy)
	if err != nil {
		return nil, fmt.Errorf("scan task-board file roles: %w", err)
	}
	if len(scan.Findings) != 0 {
		report := &LintReport{Counts: map[LintStrayKind]int{}, Structure: make([]LintStructureIssue, 0, len(scan.Findings))}
		for _, finding := range scan.Findings {
			report.Structure = append(report.Structure, LintStructureIssue{
				Path: filepath.ToSlash(filepath.Join(tasksDir, finding.Path)), Detail: finding.Detail,
			})
		}
		return report, nil
	}
	live, err := FindCardsWithPolicy(root, true, policy)
	if err != nil {
		return nil, fmt.Errorf("read live cards with file-role policy: %w", err)
	}
	all, err := FindCardsWithPolicy(root, false, policy)
	if err != nil {
		return nil, fmt.Errorf("read cards with file-role policy: %w", err)
	}
	report := lintReport(root, live, all)
	for i := range report.Dirs {
		if policy.IsModule(report.Dirs[i].Name) || policy.Parked(report.Dirs[i].Name) {
			report.Dirs[i].Kind = LintDirDeclared
		}
	}
	// Schema 4 has a complete structural classifier. The legacy directory
	// shape check would label declared modules and evidence as unknown, so it
	// is replaced rather than combined for opted-in boards.
	report.Structure = nil
	report.Clean = len(report.Strays) == 0 && len(report.Structure) == 0 &&
		len(report.IDParity) == 0
	return report, nil
}
