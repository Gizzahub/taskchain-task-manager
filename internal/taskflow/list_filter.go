package taskflow

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ListFilter selects board cards. Empty fields do not constrain.
// Priority is the card's raw frontmatter token (P0–P3), not the display label.
type ListFilter struct {
	Status   string
	Priority string
	Category string
	Kind     string
	State    string
}

// Active reports whether any filter is set.
func (f ListFilter) Active() bool {
	return f.Status != "" || f.Priority != "" || f.Category != "" || f.Kind != "" || f.State != ""
}

// FilterCards returns live board cards matching f, in walk order.
func FilterCards(root string, f ListFilter) ([]*Card, error) {
	cards, err := ListCards(root)
	if err != nil {
		return nil, err
	}
	var finished map[string]bool
	if f.State != "" {
		finished = finishedCardIDs(root)
	}
	out := make([]*Card, 0, len(cards))
	for _, card := range cards {
		if f.Status != "" && string(card.Status) != f.Status {
			continue
		}
		if f.Priority != "" && strings.TrimSpace(card.Frontmatter["priority"]) != f.Priority {
			continue
		}
		if f.Category != "" && card.Category != f.Category {
			continue
		}
		if f.Kind != "" && cardKind(card) != f.Kind {
			continue
		}
		if f.State != "" && issueState(card, finished) != f.State {
			continue
		}
		out = append(out, card)
	}
	return out, nil
}

func cardKind(card *Card) string {
	if kind, ok := canonicalKindFromID(card.ID); ok {
		return string(kind)
	}
	if kind, ok := canonicalKindFromPath(card.RepoRel()); ok {
		return string(kind)
	}
	return string(canonicalTask)
}

// issueState derives an issue card's open|blocked|promoted|resolved state.
// A non-issue card returns "" so it cannot match a state filter.
func issueState(card *Card, finished map[string]bool) string {
	if cardKind(card) != string(canonicalIssue) {
		return ""
	}
	if strings.TrimSpace(card.Frontmatter["resolution"]) != "" {
		return "resolved"
	}
	if strings.TrimSpace(card.Frontmatter["disposition"]) == "blocked" {
		return "blocked"
	}
	for _, id := range promotedIDs(card) {
		if finished == nil || !finished[id] {
			return "promoted"
		}
	}
	return "open"
}

func promotedIDs(card *Card) []string {
	if card == nil {
		return nil
	}
	fm, _ := splitFrontmatter(card.Raw)
	if fm == nil {
		return nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil
	}
	switch value := doc["promoted-to"].(type) {
	case string:
		if id := strings.TrimSpace(value); id != "" {
			return []string{id}
		}
	case []any:
		var ids []string
		for _, item := range value {
			if id := strings.TrimSpace(fmt.Sprint(item)); id != "" && id != "<nil>" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return nil
}

func finishedCardIDs(root string) map[string]bool {
	cards, err := FindCards(root, false)
	done := map[string]bool{}
	if err != nil {
		return done
	}
	for _, card := range cards {
		if card.ID == "" {
			continue
		}
		if card.Status == StatusDone || storageDirFromPath(card.RepoRel()) != "" {
			done[card.ID] = true
		}
	}
	return done
}

// RenderFilteredList writes the board with Filter lines. Callers keep the
// unfiltered path on ListText so those bytes stay unchanged.
func RenderFilteredList(w io.Writer, root string, cards []*Card, f ListFilter) {
	fmt.Fprintf(w, "=== Task List ===\n")
	fmt.Fprintf(w, "Directory: %s\n\n", filepath.Join(root, tasksDirName()))
	if f.Status != "" {
		fmt.Fprintf(w, "Filter: Status=%s\n", f.Status)
	}
	if f.Priority != "" {
		fmt.Fprintf(w, "Filter: Priority=%s\n", f.Priority)
	}
	if f.Category != "" {
		fmt.Fprintf(w, "Filter: Category=%s\n", f.Category)
	}
	if f.Kind != "" {
		fmt.Fprintf(w, "Filter: Kind=%s\n", f.Kind)
	}
	if f.State != "" {
		fmt.Fprintf(w, "Filter: State=%s\n", f.State)
	}
	if f.Active() {
		fmt.Fprintln(w)
	}
	if len(cards) == 0 {
		fmt.Fprintln(w, "No matching tasks")
		return
	}
	for _, card := range cards {
		fmt.Fprintf(w, "%s %s\n", card.Status.Symbol(), card.RepoRel())
		fmt.Fprintf(w, "   %s | %s | %s\n", card.Title, card.Priority, card.Effort)
	}
	fmt.Fprintf(w, "\nTotal: %d task(s)\n\n=== End ===\n", len(cards))
}

// RenderFilteredJSON writes the same object shape as ListJSON for a subset.
func RenderFilteredJSON(w io.Writer, root string, cards []*Card) error {
	payload := struct {
		Directory string     `json:"directory"`
		Total     int        `json:"total"`
		Tasks     []taskJSON `json:"tasks"`
	}{
		Directory: filepath.Join(root, tasksDirName()),
		Total:     len(cards),
		Tasks:     make([]taskJSON, 0, len(cards)),
	}
	for _, card := range cards {
		payload.Tasks = append(payload.Tasks, toTaskJSON(card))
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

// RenderListSummary writes the --summary aggregate. Map keys are sorted so
// the text form is deterministic; JSON object keys are sorted by encoding/json.
func RenderListSummary(w io.Writer, cards []*Card, asJSON bool) error {
	byStatus := map[string]int{}
	byPriority := map[string]int{}
	byCategory := map[string]int{}
	for _, card := range cards {
		byStatus[string(card.Status)]++
		byPriority[card.Priority]++
		if card.Category != "" {
			byCategory[card.Category]++
		}
	}
	if asJSON {
		payload := struct {
			Total      int            `json:"total"`
			ByStatus   map[string]int `json:"by_status"`
			ByPriority map[string]int `json:"by_priority"`
			ByCategory map[string]int `json:"by_category"`
		}{len(cards), byStatus, byPriority, byCategory}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
		return nil
	}
	fmt.Fprintf(w, "=== Task Summary ===\nTotal: %d tasks\n\nBy Status:\n", len(cards))
	for _, key := range sortedCountKeys(byStatus) {
		fmt.Fprintf(w, "  %s %s: %d\n", Status(key).Symbol(), key, byStatus[key])
	}
	fmt.Fprintln(w, "\nBy Priority:")
	for _, key := range sortedCountKeys(byPriority) {
		fmt.Fprintf(w, "  %s: %d\n", key, byPriority[key])
	}
	if len(byCategory) > 0 {
		fmt.Fprintln(w, "\nBy Category:")
		for _, key := range sortedCountKeys(byCategory) {
			fmt.Fprintf(w, "  %s: %d\n", key, byCategory[key])
		}
	}
	return nil
}

func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
