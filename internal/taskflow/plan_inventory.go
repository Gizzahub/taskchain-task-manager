package taskflow

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"
)

// PlanRemaining is one child that is not done or archived.
type PlanRemaining struct {
	ID   string `json:"id"`
	Zone string `json:"zone,omitempty"`
	Path string `json:"path,omitempty"`
}

// PlanPromote is the next unconverted plan row.
type PlanPromote struct {
	PlanID    string `json:"plan_id"`
	PlanPath  string `json:"plan_path"`
	ChildID   string `json:"child_id,omitempty"`
	Title     string `json:"title"`
	Effort    string `json:"effort,omitempty"`
	ExecTier  string `json:"exec_tier,omitempty"`
	Artifact  string `json:"artifact,omitempty"`
	Criterion string `json:"criterion"`
}

// PlanSummary is one incomplete plan.
type PlanSummary struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Path      string          `json:"path"`
	Total     int             `json:"total"`
	Completed int             `json:"completed"`
	Progress  int             `json:"progress"`
	Complete  bool            `json:"complete"`
	Remaining []PlanRemaining `json:"remaining,omitempty"`
}

// PlanInventory is the compact plan rollup shared by board and recommend.
type PlanInventory struct {
	Total         int
	Complete      int
	Residual      int
	ResidualPlans []PlanSummary
	Promote       *PlanPromote
}

type planJSON struct {
	Total         int           `json:"total"`
	Complete      int           `json:"complete"`
	Residual      int           `json:"residual"`
	ResidualPlans []PlanSummary `json:"residual_plans,omitempty"`
	Promote       *PlanPromote  `json:"promote,omitempty"`
}

func (p PlanInventory) json() planJSON {
	return planJSON{
		Total: p.Total, Complete: p.Complete, Residual: p.Residual,
		ResidualPlans: p.ResidualPlans, Promote: p.Promote,
	}
}

type planCardRef struct {
	Path  string
	ID    string
	Zone  string
	Title string
}

type planTableRow struct {
	Title, Effort, ExecTier, Artifact string
}

// DerivePlanInventory computes plan progress from child locations.
func DerivePlanInventory(cards []*Card) PlanInventory {
	index := indexPlanCards(cards)
	var inv PlanInventory
	for _, card := range cards {
		if card == nil || kindDir(card.TasksRel) != "plan" {
			continue
		}
		inv.Total++
		summary, promote := summarizePlan(card, index)
		if summary.Complete {
			inv.Complete++
			continue
		}
		inv.Residual++
		inv.ResidualPlans = append(inv.ResidualPlans, summary)
		if inv.Promote == nil && promote != nil {
			inv.Promote = promote
		}
	}
	return inv
}

func summarizePlan(plan *Card, index map[string]planCardRef) (PlanSummary, *PlanPromote) {
	id := plan.ID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(plan.RepoRel()), ".md")
	}
	summary := PlanSummary{ID: id, Title: plan.Title, Path: plan.RepoRel(), Remaining: []PlanRemaining{}}
	if len(plan.Children) > 0 {
		var promote *PlanPromote
		for _, child := range plan.Children {
			ref, ok := lookupPlanChild(child, index)
			summary.Total++
			if !ok {
				summary.Remaining = append(summary.Remaining, PlanRemaining{ID: child})
				if promote == nil {
					promote = promoteFromChild(plan, id, child)
				}
				continue
			}
			if ref.Zone == "done" || storageDir(strings.TrimPrefix(ref.Path, tasksDirName()+"/")) != "" {
				summary.Completed++
				continue
			}
			zone := ref.Zone
			if zone == "" {
				zone = kindDir(strings.TrimPrefix(ref.Path, tasksDirName()+"/"))
			}
			summary.Remaining = append(summary.Remaining, PlanRemaining{ID: child, Zone: zone, Path: ref.Path})
		}
		fillPlanProgress(&summary)
		return summary, promote
	}
	rows := parsePlanRows(plan.Body)
	summary.Total = len(rows)
	var promote *PlanPromote
	for _, row := range rows {
		if ref, ok := lookupPlanChild(row.Title, index); ok {
			if ref.Zone == "done" || storageDir(strings.TrimPrefix(ref.Path, tasksDirName()+"/")) != "" {
				summary.Completed++
				continue
			}
			zone := ref.Zone
			if zone == "" {
				zone = kindDir(strings.TrimPrefix(ref.Path, tasksDirName()+"/"))
			}
			summary.Remaining = append(summary.Remaining, PlanRemaining{ID: row.Title, Zone: zone, Path: ref.Path})
			continue
		}
		summary.Remaining = append(summary.Remaining, PlanRemaining{ID: row.Title})
		if promote == nil {
			promote = promoteFromRow(plan, id, row)
		}
	}
	fillPlanProgress(&summary)
	return summary, promote
}

func fillPlanProgress(summary *PlanSummary) {
	if summary.Total > 0 {
		summary.Progress = summary.Completed * 100 / summary.Total
	}
	summary.Complete = summary.Total > 0 && len(summary.Remaining) == 0
}

func promoteFromChild(plan *Card, planID, child string) *PlanPromote {
	return &PlanPromote{
		PlanID: planID, PlanPath: plan.RepoRel(), ChildID: child, Title: child,
		Criterion: promoteCriterion(child, ""),
	}
}

func promoteFromRow(plan *Card, planID string, row planTableRow) *PlanPromote {
	return &PlanPromote{
		PlanID: planID, PlanPath: plan.RepoRel(), Title: row.Title,
		Effort: row.Effort, ExecTier: row.ExecTier, Artifact: row.Artifact,
		Criterion: promoteCriterion(row.Title, row.Artifact),
	}
}

func promoteCriterion(title, artifact string) string {
	if artifact != "" {
		return artifact + " exists | verify: human — confirm the artifact"
	}
	if title == "" {
		title = "plan row"
	}
	return title + " delivered | verify: human — confirm the plan row"
}

func indexPlanCards(cards []*Card) map[string]planCardRef {
	index := map[string]planCardRef{}
	for _, card := range cards {
		if card == nil || kindDir(card.TasksRel) == "plan" {
			continue
		}
		ref := planCardRef{Path: card.RepoRel(), ID: card.ID, Title: card.Title, Zone: card.Zone}
		if ref.Zone == "" {
			if st, ok := StatusFromDir(card.Category); ok {
				ref.Zone = st.Dir()
			}
		}
		put := func(key string) {
			key = strings.TrimSpace(key)
			if key != "" {
				if _, exists := index[key]; !exists {
					index[key] = ref
				}
			}
		}
		put(card.ID)
		put(strings.ToLower(card.Title))
		base := strings.TrimSuffix(filepath.Base(card.RepoRel()), ".md")
		put(base)
		if n := leadingNumber(base); n != "" {
			put(n)
		}
	}
	return index
}

func lookupPlanChild(child string, index map[string]planCardRef) (planCardRef, bool) {
	child = strings.TrimSpace(child)
	if child == "" {
		return planCardRef{}, false
	}
	if ref, ok := index[child]; ok {
		return ref, true
	}
	if ref, ok := index[strings.ToLower(child)]; ok {
		return ref, true
	}
	if _, num, ok := strings.Cut(child, "-"); ok && num != "" {
		if ref, ok := index[num]; ok {
			return ref, true
		}
	}
	return planCardRef{}, false
}

func leadingNumber(name string) string {
	i := 0
	for i < len(name) && unicode.IsDigit(rune(name[i])) {
		i++
	}
	if i == 0 {
		return ""
	}
	return name[:i]
}

func parsePlanRows(body string) []planTableRow {
	lines := strings.Split(body, "\n")
	titleCol, effortCol, tierCol, artifactCol := -1, -1, -1, -1
	start := -1
	for i, line := range lines {
		if !strings.Contains(line, "|") {
			continue
		}
		cells := tableCells(line)
		if len(cells) < 2 {
			continue
		}
		joined := strings.ToLower(strings.Join(cells, " "))
		if !strings.Contains(joined, "작업단위") && !strings.Contains(joined, "work unit") {
			continue
		}
		for c, cell := range cells {
			switch strings.ToLower(strings.TrimSpace(cell)) {
			case "작업단위", "work unit", "unit":
				titleCol = c
			case "effort":
				effortCol = c
			case "exec-tier", "exec tier":
				tierCol = c
			case "산출물", "artifact", "output":
				artifactCol = c
			}
		}
		if titleCol < 0 {
			continue
		}
		start = i + 1
		if start < len(lines) && isTableSep(lines[start]) {
			start++
		}
		break
	}
	if start < 0 {
		return nil
	}
	var rows []planTableRow
	for _, line := range lines[start:] {
		if !strings.Contains(line, "|") || isTableSep(line) {
			if len(rows) > 0 {
				break
			}
			continue
		}
		cells := tableCells(line)
		if titleCol >= len(cells) {
			continue
		}
		title := strings.TrimSpace(cells[titleCol])
		if title == "" || strings.HasPrefix(title, "[") {
			continue
		}
		row := planTableRow{Title: title}
		if effortCol >= 0 && effortCol < len(cells) {
			row.Effort = strings.TrimSpace(cells[effortCol])
		}
		if tierCol >= 0 && tierCol < len(cells) {
			row.ExecTier = strings.Trim(strings.TrimSpace(cells[tierCol]), "`")
		}
		if artifactCol >= 0 && artifactCol < len(cells) {
			row.Artifact = strings.TrimSpace(cells[artifactCol])
		}
		rows = append(rows, row)
	}
	return rows
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "|")
	raw := strings.Split(line, "|")
	out := make([]string, 0, len(raw))
	for _, cell := range raw {
		out = append(out, strings.TrimSpace(cell))
	}
	return out
}

func isTableSep(line string) bool {
	stripped := strings.ReplaceAll(strings.TrimSpace(line), "|", "")
	stripped = strings.ReplaceAll(stripped, ":", "")
	stripped = strings.ReplaceAll(stripped, "-", "")
	stripped = strings.ReplaceAll(stripped, " ", "")
	return stripped == "" && strings.Contains(line, "-")
}

func writePlanInventoryText(w io.Writer, plans PlanInventory) {
	if plans.Total == 0 {
		return
	}
	fmt.Fprintf(w, "plans: %d complete, %d residual\n", plans.Complete, plans.Residual)
	for _, plan := range plans.ResidualPlans {
		line := fmt.Sprintf("  %s %d/%d", plan.ID, plan.Completed, plan.Total)
		if len(plan.Remaining) > 0 {
			parts := make([]string, 0, len(plan.Remaining))
			for _, remaining := range plan.Remaining {
				if remaining.Zone != "" {
					parts = append(parts, remaining.Zone+":"+remaining.ID)
				} else {
					parts = append(parts, remaining.ID)
				}
			}
			line += "  remaining " + strings.Join(parts, ", ")
		}
		fmt.Fprintln(w, line)
	}
	if plans.Promote != nil {
		fmt.Fprintf(w, "  next row: %s (%s)\n", plans.Promote.Title, plans.Promote.PlanID)
	}
}
