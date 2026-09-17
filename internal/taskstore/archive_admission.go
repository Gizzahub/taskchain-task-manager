package taskstore

import (
	"fmt"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// observeArchiveAdmission binds the pure evaluator to explicit source scope
// and metadata. resolve must inspect the same locked board snapshot as raw.
// This dialect only projects explicit ID references; a legacy title/body
// adapter must resolve a complete census before adopting that dialect.
func observeArchiveAdmission(raw []byte, id, source string, p boardpolicy.Policy, cfg archivepolicy.Config, resolve func(string) (bool, error)) (archivepolicy.Decision, error) {
	var result archivepolicy.Decision
	if len(raw) == 0 || len(raw) > maxCardBytes || identityKey(id) == "" {
		return result, fmt.Errorf("invalid archive card size or identity")
	}
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	zone, _, err := archiveDestination(source, p)
	if err != nil {
		return result, err
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return result, err
	}
	v := doc.View()
	if v.ID != id {
		return result, fmt.Errorf("archive admission raw identity mismatch")
	}
	if strings.EqualFold(v.Status, "superseded") {
		return result, fmt.Errorf("superseded card cannot enter normal archive admission")
	}
	m, err := doc.ProjectArchiveMetadata(cfg.Admission.Fields.Mapping())
	if err != nil {
		return result, err
	}
	value := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	f := archivepolicy.Facts{ID: id, Kind: "task", Status: v.Status, Review: value(m.QualityReview), Evidence: value(m.QualityReviewEvidence), Resolution: value(m.Resolution)}
	if status, ok := p.Status(zone); ok {
		f.Status = status
	}
	f.WorkflowDone = p.Workflow(zone) && zone == p.DoneZone()
	switch zone {
	case "plan":
		f.Kind = "plan"
		f.References = m.Children
	case "issue":
		f.Kind = "issue"
		f.References = m.PromotedTo
	case "backlog":
		f.Kind = "backlog"
	}
	return archivepolicy.EvaluateNormal(archivepolicy.Rules{AcceptedReviews: cfg.Admission.AcceptedReviews}, f, resolve)
}
