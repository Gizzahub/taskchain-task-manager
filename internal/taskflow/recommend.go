package taskflow

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	emptyReasonNoPending      = "no-pending-cards"
	emptyReasonOnlyBlocked    = "only-blocked"
	emptyReasonPromotePlanRow = "promote-plan-row"
	emptyReasonPlansResidual  = "plans-residual"
	emptyReasonNoQueue        = "no-queue-in-scope"

	reasonDangling     = "dangling-dependency"
	reasonCaseMismatch = "case-mismatch-dependency"
)

// RecommendOptions are the recommend filters. Limit 0 does not truncate.
type RecommendOptions struct {
	Category  string
	MaxEffort string
	Limit     int
}

// DependencyFault is a depends-on that waiting will not satisfy.
type DependencyFault struct {
	Path       string
	ID         string
	Dependency string
	Reason     string
}

// Recommendation is one pending card and the score it ranked by.
type Recommendation struct {
	Card  *Card
	Score int
}

// RecommendReport is the ranking plus why an empty ranking is empty.
type RecommendReport struct {
	Recommendations  []Recommendation
	EmptyReason      string
	Blocked          int
	Plans            PlanInventory
	MissingZones     []string
	DependencyFaults []DependencyFault
}

// Recommend ranks pending cards. A missing tasks directory is an empty board.
// A tasks path that cannot be opened is an error.
func Recommend(root string, opts RecommendOptions) (*RecommendReport, error) {
	if err := readableTasksDir(root); err != nil {
		return nil, err
	}
	live, err := FindCards(root, true)
	if err != nil {
		return nil, err
	}
	all, err := FindCards(root, false)
	if err != nil {
		return nil, err
	}
	index := buildCompletionIndex(all)
	recs, faults := rankPending(live, index, opts)

	blocked := 0
	for _, card := range all {
		if card != nil && card.Status == StatusBlocked {
			blocked++
		}
	}
	report := &RecommendReport{
		Recommendations:  recs,
		Blocked:          blocked,
		Plans:            DerivePlanInventory(all),
		DependencyFaults: faults,
	}
	if len(recs) > 0 {
		return report, nil
	}
	switch {
	case report.Plans.Promote != nil:
		report.EmptyReason = emptyReasonPromotePlanRow
	case blocked > 0:
		report.EmptyReason = emptyReasonOnlyBlocked
	case report.Plans.Residual > 0:
		report.EmptyReason = emptyReasonPlansResidual
	default:
		lint := Lint(root)
		if missing := preflightMissingWorkflowZones(nil, lint); len(missing) > 0 {
			report.EmptyReason = emptyReasonNoQueue
			report.MissingZones = missing
		} else {
			report.EmptyReason = emptyReasonNoPending
		}
	}
	return report, nil
}

func rankPending(cards []*Card, index *completionIndex, opts RecommendOptions) ([]Recommendation, []DependencyFault) {
	var recs []Recommendation
	var faults []DependencyFault
	for _, card := range cards {
		if card == nil || card.Status != StatusPending {
			continue
		}
		if opts.Category != "" && card.Category != opts.Category {
			continue
		}
		if opts.Category == "" && isHoldingZone(card.Category) {
			continue
		}
		if opts.MaxEffort != "" && !effortWithin(rawEffort(card), opts.MaxEffort) {
			continue
		}
		if gaps := index.gaps(card.DependsOn); !gaps.empty() {
			faults = append(faults, faultsFor(card, gaps)...)
			continue
		}
		recs = append(recs, Recommendation{Card: card, Score: card.Score()})
	}
	// Stable so equal scores keep the lexical walk order.
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Score > recs[j].Score })
	if opts.Limit > 0 && len(recs) > opts.Limit {
		recs = recs[:opts.Limit]
	}
	return recs, faults
}

func faultsFor(card *Card, gaps dependencyGaps) []DependencyFault {
	var faults []DependencyFault
	for _, dep := range gaps.Dangling {
		faults = append(faults, DependencyFault{Path: card.RepoRel(), ID: card.ID, Dependency: dep, Reason: reasonDangling})
	}
	for _, dep := range gaps.CaseMismatch {
		faults = append(faults, DependencyFault{Path: card.RepoRel(), ID: card.ID, Dependency: dep, Reason: reasonCaseMismatch})
	}
	return faults
}

func isHoldingZone(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "backlog", "someday", "icebox":
		return true
	}
	return false
}

// rawEffort ranks the frontmatter token, not the display parser. An
// unrecognised token is rank 0 and passes every max-effort filter.
func rawEffort(card *Card) string {
	if card == nil || card.Frontmatter == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(card.Frontmatter["effort"]))
}

func effortRank(token string) int {
	switch strings.ToUpper(strings.TrimSpace(token)) {
	case "XS":
		return 1
	case "S":
		return 2
	case "M":
		return 3
	case "L":
		return 4
	case "XL":
		return 5
	}
	return 0
}

func effortWithin(effort, maxEffort string) bool {
	return effortRank(effort) <= effortRank(maxEffort)
}

// RenderRecommendText writes the text report, including the banner.
func RenderRecommendText(w io.Writer, report *RecommendReport) {
	if report == nil {
		report = &RecommendReport{}
	}
	fmt.Fprintln(w, "🔍 Finding next recommended tasks...")
	fmt.Fprintln(w)
	if len(report.DependencyFaults) > 0 {
		writeRecommendFaults(w, report.DependencyFaults)
	}
	if len(report.Recommendations) == 0 {
		writeRecommendEmpty(w, report)
		return
	}
	for _, rec := range report.Recommendations {
		card := rec.Card
		fmt.Fprintln(w, "---")
		fmt.Fprintf(w, "📋 %s\n", card.Title)
		fmt.Fprintf(w, "   File: %s\n", card.RepoRel())
		fmt.Fprintf(w, "   Priority: %s | Effort: %s | Score: %d\n", card.Priority, card.Effort, rec.Score)
		fmt.Fprintln(w)
		criteria := card.Criteria()
		if len(criteria) > 0 {
			fmt.Fprintln(w, "   Acceptance Criteria:")
			limit := len(criteria)
			if limit > 3 {
				limit = 3
			}
			for _, criterion := range criteria[:limit] {
				fmt.Fprintf(w, "   - %s\n", criterion.Text)
			}
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "💡 Tip: Review the top task and start with:")
	fmt.Fprintln(w, "   1. Open the task file")
	fmt.Fprintln(w, "   2. Update Status to [~] In Progress")
	fmt.Fprintln(w, "   3. Begin work")
}

func writeRecommendFaults(w io.Writer, faults []DependencyFault) {
	fmt.Fprintln(w, "DEPENDENCY FAULTS (excluded from the ranking; waiting will not resolve these)")
	seen := map[string]bool{}
	for _, fault := range faults {
		fmt.Fprintf(w, "  %-24s %s -> %s\n", fault.Reason, fault.Path, fault.Dependency)
		seen[fault.Reason] = true
	}
	help := map[string]string{
		reasonDangling:     "a `depends-on` names an id no card carries -- fix the reference; waiting will not resolve it",
		reasonCaseMismatch: "a `depends-on` names a card that exists but is spelled with the wrong case -- fix the spelling",
	}
	for _, reason := range []string{reasonDangling, reasonCaseMismatch} {
		if seen[reason] {
			fmt.Fprintf(w, "  %-24s %s\n", "", help[reason])
		}
	}
	fmt.Fprintln(w)
}

func writeRecommendEmpty(w io.Writer, report *RecommendReport) {
	if report.EmptyReason == emptyReasonNoQueue {
		fmt.Fprintf(w, "No pending tasks (%s): %s under the tasks root.\n", report.EmptyReason, missingZonesClause(report.MissingZones))
		fmt.Fprintln(w, "This is NOT a drained queue -- there is nowhere in scope for a card to be,")
		fmt.Fprintln(w, "so recommend cannot surface one however much work the repository has.")
		fmt.Fprintln(w, "Run `ce task preflight` for the full picture (strays, declared parking")
		fmt.Fprintln(w, "zones) or `ce task new task --title \"...\"` to open the todo zone for")
		fmt.Fprintln(w, "real work -- the zone directory is created by writing the first card into it.")
	} else {
		fmt.Fprintf(w, "No pending tasks (%s).\n", report.EmptyReason)
	}
	if report.Blocked > 0 {
		fmt.Fprintf(w, "blocked: %d\n", report.Blocked)
	}
	writePlanInventoryText(w, report.Plans)
}

func missingZonesClause(zones []string) string {
	verb := "do not exist"
	if len(zones) == 1 {
		verb = "does not exist"
	}
	return joinZoneDirs(zones) + " " + verb
}

func joinZoneDirs(zones []string) string {
	if len(zones) == 0 {
		return "the scoped workflow zone(s)"
	}
	dirs := make([]string, 0, len(zones))
	for _, zone := range zones {
		dirs = append(dirs, "tasks/"+zone+"/")
	}
	if len(dirs) == 1 {
		return dirs[0]
	}
	return strings.Join(dirs[:len(dirs)-1], ", ") + " and " + dirs[len(dirs)-1]
}

// RenderRecommendJSON writes the indented recommend document. Items are the
// list task fields plus score. There is no outputVersion.
func RenderRecommendJSON(w io.Writer, report *RecommendReport) error {
	if report == nil {
		report = &RecommendReport{}
	}
	payload := recommendJSON{
		Total:           len(report.Recommendations),
		EmptyReason:     report.EmptyReason,
		Blocked:         report.Blocked,
		Plans:           report.Plans.json(),
		Recommendations: []recommendItemJSON{},
		MissingZones:    report.MissingZones,
	}
	for _, fault := range report.DependencyFaults {
		payload.Faults = append(payload.Faults, faultJSON{
			Path: fault.Path, ID: fault.ID, Dependency: fault.Dependency, Reason: fault.Reason,
		})
	}
	for _, rec := range report.Recommendations {
		payload.Recommendations = append(payload.Recommendations, toRecommendItemJSON(rec))
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

// recommendItemJSON is the list task fields plus score. Unlike list it
// carries exec_tier and depends_on when the card sets them, as the
// recommend contract names them; list output is left unchanged.
type recommendItemJSON struct {
	Path               string   `json:"path"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	Priority           string   `json:"priority"`
	Effort             string   `json:"effort"`
	Category           string   `json:"category"`
	Created            string   `json:"created"`
	ExecTier           string   `json:"exec_tier,omitempty"`
	DependsOn          []string `json:"depends_on,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Score              int      `json:"score"`
}

func toRecommendItemJSON(rec Recommendation) recommendItemJSON {
	base := toTaskJSON(rec.Card)
	return recommendItemJSON{
		Path:               base.Path,
		Title:              base.Title,
		Status:             base.Status,
		Priority:           base.Priority,
		Effort:             base.Effort,
		Category:           base.Category,
		Created:            base.Created,
		ExecTier:           rec.Card.ExecTier,
		DependsOn:          rec.Card.DependsOn,
		AcceptanceCriteria: base.AcceptanceCriteria,
		Score:              rec.Score,
	}
}

type faultJSON struct {
	Path       string `json:"path"`
	ID         string `json:"id,omitempty"`
	Dependency string `json:"dependency"`
	Reason     string `json:"reason"`
}

type recommendJSON struct {
	Total           int                 `json:"total"`
	EmptyReason     string              `json:"empty_reason,omitempty"`
	Blocked         int                 `json:"blocked,omitempty"`
	Plans           planJSON            `json:"plans"`
	Recommendations []recommendItemJSON `json:"recommendations"`
	MissingZones    []string            `json:"missing_zones,omitempty"`
	Faults          []faultJSON         `json:"dependency_faults,omitempty"`
}
