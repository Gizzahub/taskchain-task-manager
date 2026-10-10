package taskflow

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// stateZone is the directory each state action selects from.
var stateZone = map[string]string{
	"todo-run":        "todo",
	"doing-resume":    "doing",
	"review-run":      "review",
	"done-finalize":   "done",
	"blocked-recover": "blocked",
	"issue-promote":   "issue",
	"plan-promote":    "plan",
	"backlog-promote": "backlog",
}

// StateCandidate is one eligible card. JSON keys are tagged; empty lists are
// arrays. Skill is not a field of the candidate.
type StateCandidate struct {
	Path                  string   `json:"path"`
	Title                 string   `json:"title"`
	Status                string   `json:"status"`
	Priority              string   `json:"priority"`
	Effort                string   `json:"effort"`
	Created               string   `json:"created"`
	Description           string   `json:"description"`
	AcceptanceCriteria    []string `json:"acceptanceCriteria"`
	Category              string   `json:"category"`
	Zone                  string   `json:"zone"`
	ID                    string   `json:"id"`
	Children              []string `json:"children"`
	Parent                string   `json:"parent"`
	DependsOn             []string `json:"dependsOn"`
	Blocks                []string `json:"blocks"`
	ExecTier              string   `json:"execTier"`
	Agent                 string   `json:"agent"`
	QualityReview         string   `json:"qualityReview"`
	QualityReviewEvidence string   `json:"qualityReviewEvidence"`
	Resolution            string   `json:"resolution"`
	PromotedTo            []string `json:"promotedTo"`
	Disposition           string   `json:"disposition"`
	BlockedBy             []string `json:"blockedBy"`
}

// StateResult is the handoff envelope. Skill is empty until a board policy
// declares a zone-skill mapping; callers omit the key when it is empty.
type StateResult struct {
	Action     string           `json:"action"`
	Zone       string           `json:"zone"`
	Skill      string           `json:"skill,omitempty"`
	Status     string           `json:"status"`
	Candidates []StateCandidate `json:"candidates"`
	NextAction string           `json:"next_action"`
}

// SelectState chooses eligible cards. It does not write. A named
// done-finalize card is the caller's to refuse: this function only selects.
func SelectState(root, action string, selected []string) (*StateResult, error) {
	zone, ok := stateZone[action]
	if !ok {
		return nil, fmt.Errorf("unknown state action %q", action)
	}
	if err := readableTasksDir(root); err != nil {
		return nil, err
	}
	cards, err := LoadLiveCards(root)
	if err != nil {
		return nil, err
	}
	chosen, err := stateCandidates(root, cards, zone, selected)
	if err != nil {
		return nil, err
	}
	narrow := len(selected) == 0 && (action == "issue-promote" || action == "todo-run" || action == "review-run")
	if action == "review-run" && len(selected) > 0 {
		chosen, err = orderReview(chosen)
		if err != nil {
			return nil, err
		}
	} else if narrow {
		chosen = highestScore(chosen)
	}
	result := &StateResult{
		Action:     action,
		Zone:       zone,
		Status:     "selection-required",
		Candidates: []StateCandidate{},
		NextAction: "Choose only an eligible card in the stated zone; do not infer authority from a score.",
	}
	if len(selected) > 0 {
		result.Status = "agent-handoff"
		result.NextAction = "Invoke the referenced Skill with repository authority and verification contract; recheck the card zone before changes."
	}
	if len(chosen) == 0 {
		result.Status = "empty"
	}
	for _, card := range chosen {
		result.Candidates = append(result.Candidates, toStateCandidate(card))
	}
	return result, nil
}

func stateCandidates(root string, cards []*Card, zone string, selected []string) ([]*Card, error) {
	seen := map[string]string{}
	matched := map[string]bool{}
	var chosen []*Card
	for _, card := range cards {
		if card.ID != "" {
			if previous, exists := seen[card.ID]; exists {
				return nil, fmt.Errorf("duplicate card ID %s: %s and %s", card.ID, previous, card.RepoRel())
			}
			seen[card.ID] = card.RepoRel()
		}
		wanted := len(selected) == 0
		for _, target := range selected {
			if matchesTarget(root, card, target) {
				matched[target] = true
				wanted = true
			}
		}
		if !wanted {
			continue
		}
		if !eligibleForZone(card, zone) {
			if len(selected) > 0 {
				return nil, fmt.Errorf("%s is outside %s/; select the command for its actual state (no fallback)", card.RepoRel(), zone)
			}
			continue
		}
		if zone == "issue" && card.IssueState(nil) != "open" {
			if len(selected) > 0 {
				return nil, fmt.Errorf("issue is not open: %s", card.RepoRel())
			}
			continue
		}
		chosen = append(chosen, card)
	}
	for _, target := range selected {
		if !matched[target] {
			return nil, fmt.Errorf("explicit card not found in live board: %s (no fallback)", target)
		}
	}
	return chosen, nil
}

func eligibleForZone(card *Card, zone string) bool {
	switch zone {
	case "issue", "plan", "backlog":
		prefix := strings.ToUpper(zone) + "-"
		return strings.HasPrefix(card.ID, prefix) && strings.HasPrefix(card.TasksRel, zone+"/")
	default:
		return card.Zone == zone
	}
}

func matchesTarget(root string, card *Card, target string) bool {
	if card.ID != "" && target == card.ID {
		return true
	}
	want := filepath.ToSlash(filepath.Clean(target))
	if filepath.IsAbs(target) {
		if rel, err := filepath.Rel(root, target); err == nil {
			want = filepath.ToSlash(filepath.Clean(rel))
		}
	}
	return want == filepath.ToSlash(filepath.Clean(card.RepoRel()))
}

func highestScore(cards []*Card) []*Card {
	if len(cards) <= 1 {
		return cards
	}
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Score() != cards[j].Score() {
			return cards[i].Score() > cards[j].Score()
		}
		if cards[i].ID != cards[j].ID {
			return cards[i].ID < cards[j].ID
		}
		return cards[i].RepoRel() < cards[j].RepoRel()
	})
	return cards[:1]
}

func orderReview(cards []*Card) ([]*Card, error) {
	byDependency := map[string]*Card{}
	for _, card := range cards {
		if card.ID != "" {
			byDependency[card.ID] = card
		}
		byDependency[filepath.Base(card.RepoRel())] = card
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	ordered := make([]*Card, 0, len(cards))
	var visit func(*Card) error
	visit = func(card *Card) error {
		key := card.RepoRel()
		if visited[key] {
			return nil
		}
		if visiting[key] {
			return fmt.Errorf("review-run selected cards have a dependency cycle at %s", card.RepoRel())
		}
		visiting[key] = true
		deps := append([]string(nil), card.DependsOn...)
		sort.Strings(deps)
		for _, dependency := range deps {
			dependency = strings.TrimPrefix(dependency, "file:")
			if predecessor := byDependency[filepath.Base(dependency)]; predecessor != nil {
				if err := visit(predecessor); err != nil {
					return err
				}
			}
		}
		visiting[key] = false
		visited[key] = true
		ordered = append(ordered, card)
		return nil
	}
	sorted := append([]*Card(nil), cards...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RepoRel() < sorted[j].RepoRel() })
	for _, card := range sorted {
		if err := visit(card); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func toStateCandidate(card *Card) StateCandidate {
	criteria := []string{}
	for _, criterion := range card.Criteria() {
		criteria = append(criteria, criterion.Text)
	}
	zone := card.Zone
	if zone == "" {
		zone = kindDir(card.TasksRel)
	}
	return StateCandidate{
		Path: card.RepoRel(), Title: card.Title, Status: string(card.Status),
		Priority: card.Priority, Effort: card.Effort, Created: card.Created,
		Description: card.description(), AcceptanceCriteria: criteria,
		Category: card.Category, Zone: zone, ID: card.ID,
		Children: orEmpty(card.Children), Parent: card.Parent,
		DependsOn: orEmpty(card.DependsOn), Blocks: orEmpty(card.Blocks),
		ExecTier: card.ExecTier, Agent: card.Agent,
		QualityReview: card.QualityReview, QualityReviewEvidence: card.QualityReviewEvidence,
		Resolution: card.Resolution, PromotedTo: orEmpty(card.PromotedTo),
		Disposition: card.Disposition, BlockedBy: orEmpty(card.BlockedBy),
	}
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// RenderStateText writes the handoff. An empty skill omits the skill clause.
func RenderStateText(w io.Writer, result *StateResult) {
	if result == nil {
		return
	}
	if result.Skill == "" {
		fmt.Fprintf(w, "%s: %s (zone=%s)\n", result.Action, result.Status, result.Zone)
	} else {
		fmt.Fprintf(w, "%s: %s (zone=%s, skill=%s)\n", result.Action, result.Status, result.Zone, result.Skill)
	}
	for _, card := range result.Candidates {
		fmt.Fprintf(w, "  %s %s\n", card.ID, card.Path)
	}
	fmt.Fprintln(w, result.NextAction)
}
