package card

import (
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

// CompletionReport observes a single card. It does not grant permission to
// transition, authenticate evidence, or attest that implementation is complete.
type CompletionReport struct {
	OutputVersion      int                         `json:"outputVersion"`
	Scope              outputvocab.Scope           `json:"scope"`
	CardValid          bool                        `json:"cardValid"`
	CriteriaComplete   bool                        `json:"criteriaComplete"`
	Valid              bool                        `json:"valid"`
	EvidenceValidation outputvocab.ValidationState `json:"evidenceValidation"`
	BoardValidation    outputvocab.ValidationState `json:"boardValidation"`
	Criteria           []CriterionReport           `json:"criteria"`
	Findings           []ValidationFinding         `json:"findings"`
}

func (d *Document) ValidateCompletion(path string, rules ValidationRules) (CompletionReport, error) {
	card, err := d.ValidateCard(path, rules)
	if err != nil {
		return CompletionReport{}, err
	}
	_, body, err := splitFrontmatter(d.raw)
	if err != nil {
		return CompletionReport{}, err
	}
	criteria, _, found, malformed := criteriaFromBodyMode(string(body), rules, true)
	complete := found && len(criteria) > 0 && !malformed
	for _, criterion := range criteria {
		complete = complete && criterion.Checked
	}
	findings := append([]ValidationFinding{}, card.Findings...)
	if malformed {
		findings = append(findings, ValidationFinding{Severity: outputvocab.SeverityError, Field: "completion-criteria", Message: "unsupported or malformed checkbox syntax in criteria section"})
	}
	if !complete {
		findings = append(findings, ValidationFinding{Severity: outputvocab.SeverityError, Field: "completion", Message: "completion requires nonempty, well-formed criteria with every item checked"})
	}
	return CompletionReport{
		OutputVersion: outputformat.Version, Scope: outputvocab.ScopeCardCompletionObservation, CardValid: card.Valid,
		CriteriaComplete: complete, Valid: card.Valid && complete,
		EvidenceValidation: outputvocab.NotEvaluated, BoardValidation: outputvocab.NotEvaluated,
		Criteria: criteria, Findings: findings,
	}, nil
}
