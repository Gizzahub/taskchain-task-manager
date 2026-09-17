// Package archivepolicy evaluates explicit archive admission without I/O.
package archivepolicy

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

// Rules is explicit: no review vocabulary is silently installed for a board.
// Review matching is case-insensitive after trimming whitespace.
type Rules struct {
	AcceptedReviews []string
}

// Facts must come from a locked, validated board snapshot. Kind is the source
// zone's semantic kind, not the ID prefix. References are supplied by the
// caller's declared children/promotion projection, never inferred here.
type Facts struct {
	ID, Kind, Status, Review, Evidence, Resolution string
	WorkflowDone                                   bool
	References                                     []string
}

// Decision is structural admission, not a claim that implementation or tests
// succeeded. CompletionEligible still requires a durable exact card binding.
type Decision struct {
	Allowed            bool
	CompletionEligible bool
	Provenance         string
	Reasons            []string
}

// EvaluateNormal never implements force, supersede, or legacy adoption. Those
// operations must retain distinct authorization and provenance.
func EvaluateNormal(r Rules, f Facts, complete func(string) (bool, error)) (Decision, error) {
	out := Decision{Provenance: "normal-archive", Reasons: []string{}}
	if len(r.AcceptedReviews) == 0 {
		return out, fmt.Errorf("archive rules require accepted review values")
	}
	accepted := map[string]bool{}
	for _, value := range r.AcceptedReviews {
		key := strings.ToLower(value)
		if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") || accepted[key] {
			return out, fmt.Errorf("invalid or duplicate archive review value %q", value)
		}
		accepted[key] = true
	}
	id, err := cardid.Parse(f.ID)
	if err != nil {
		return out, err
	}
	deny := func(reason string) { out.Reasons = append(out.Reasons, reason) }
	switch f.Kind {
	case "task", "backlog":
		if f.Status != "done" {
			deny("work card is not done")
		}
		if !accepted[strings.ToLower(strings.TrimSpace(f.Review))] {
			deny("review verdict is not accepted")
		}
		if strings.TrimSpace(f.Evidence) == "" {
			deny("review evidence is empty")
		}
	case "plan":
		if len(f.References) == 0 {
			deny("plan has no children")
		}
	case "issue":
		if strings.TrimSpace(f.Resolution) == "" {
			deny("issue resolution is empty")
		}
	default:
		return out, fmt.Errorf("unsupported archive source kind %q", f.Kind)
	}
	if f.Kind == "plan" || f.Kind == "issue" {
		seen := map[string]bool{}
		for _, ref := range f.References {
			parsed, err := cardid.Parse(ref)
			if err != nil {
				return out, fmt.Errorf("archive reference: %w", err)
			}
			if parsed.Key() == id.Key() || seen[parsed.Key()] {
				return out, fmt.Errorf("self or duplicate archive reference %q", ref)
			}
			seen[parsed.Key()] = true
			if complete == nil {
				return out, fmt.Errorf("archive references require a completion resolver")
			}
			done, err := complete(ref)
			if err != nil {
				return out, fmt.Errorf("resolve archive reference %s: %w", ref, err)
			}
			if !done {
				deny("reference is not complete: " + ref)
			}
		}
	}
	out.Allowed = len(out.Reasons) == 0
	out.CompletionEligible = out.Allowed && id.Prefix == "TASK" && f.Kind == "task" && f.WorkflowDone && f.Status == "done"
	return out, nil
}
