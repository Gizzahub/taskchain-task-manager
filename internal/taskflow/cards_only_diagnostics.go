package taskflow

import (
	"fmt"
	"path"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
)

// CardsOnlyLintDiagnostics maps an explicit inactive assessment to the legacy
// lint envelope. No CLI/gate calls it until an adoption wire is accepted. It
// judges structure only, not card validity, identity or complete board readiness.
func CardsOnlyLintDiagnostics(tasksDir string, findings []boardfiles.SnapshotFinding) *LintReport {
	report := &LintReport{Counts: map[LintStrayKind]int{}, Clean: true}
	for _, finding := range findings {
		message := cardsOnlyMessage(finding)
		if finding.Severity != boardfiles.SnapshotIssueResidue {
			report.Structure = append(report.Structure, LintStructureIssue{Path: path.Join(tasksDir, finding.Path), Detail: message})
			report.Clean = false
		} else {
			report.Warnings = append(report.Warnings, LintWarning{Kind: "local-residue", Detail: path.Join(tasksDir, finding.Path) + ": " + message})
		}
	}
	return report
}

// CardsOnlyValidationWarnings maps the SAME assessment to advisory results.
// nil selected means all paths; a nonnil list selects the named paths and
// their pruned ancestor findings. ValidateStaged's legacy filter is unchanged.
func CardsOnlyValidationWarnings(tasksDir string, findings []boardfiles.SnapshotFinding, selected []string) []ValidationResult {
	wanted := map[string]bool{}
	for _, rel := range selected {
		wanted[rel] = true
	}
	results := []ValidationResult{}
	for _, finding := range findings {
		if selected != nil && !wanted[finding.Path] {
			ancestor := false
			for rel := range wanted {
				ancestor = ancestor || strings.HasPrefix(rel, finding.Path+"/")
			}
			if !ancestor {
				continue
			}
		}
		results = append(results, ValidationResult{
			Path:     path.Join(tasksDir, finding.Path),
			Warnings: []Finding{{Field: "board-structure", Message: cardsOnlyMessage(finding)}},
		})
	}
	return results
}

func cardsOnlyMessage(finding boardfiles.SnapshotFinding) string {
	return fmt.Sprintf("%s; contract: %s; action: %s", finding.Detail, finding.Contract, finding.Action)
}
