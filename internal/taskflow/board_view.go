package taskflow

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const defaultStaleHours = 24

// DefaultStaleHours is the doing-claim window when --stale-hours is omitted.
func DefaultStaleHours() int { return defaultStaleHours }

// BoardTask is one card on a claim-board column. Age and Stale apply only to doing.
type BoardTask struct {
	Card  *Card
	Age   time.Duration
	Stale bool
}

// BoardZone is one column, in display order.
type BoardZone struct {
	Name  string
	Tasks []BoardTask
}

var boardZoneOrder = []string{"doing", "todo", "review", "blocked", "done"}

// BoardZoneOf is the column name. A workflow zone wins; otherwise the category
// is folded when it is a zone spelling; otherwise the card is unzoned.
func BoardZoneOf(card *Card) string {
	if card == nil {
		return "(unzoned)"
	}
	zone := card.Zone
	if zone == "" {
		if st, ok := StatusFromDir(card.Category); ok {
			zone = st.Dir()
		} else {
			zone = card.Category
		}
	}
	if zone == "" {
		return "(unzoned)"
	}
	return zone
}

// ModuleLabel is the category when it names a module rather than the column.
func ModuleLabel(card *Card, column string) string {
	if card == nil {
		return ""
	}
	mod := card.Category
	if mod == "" || mod == column {
		return ""
	}
	if _, isZone := StatusFromDir(mod); isZone {
		return ""
	}
	return mod
}

// ClassifyBoard groups cards into columns and flags stale doing claims.
// Plan cards and storage cards are not columns. staleAfter <= 0 disables flags.
func ClassifyBoard(cards []*Card, mtimes map[string]time.Time, now time.Time, staleAfter time.Duration) []BoardZone {
	byZone := map[string][]BoardTask{}
	for _, card := range cards {
		if card == nil || kindDir(card.TasksRel) == "plan" || storageDir(card.TasksRel) != "" {
			continue
		}
		zone := BoardZoneOf(card)
		item := BoardTask{Card: card}
		if zone == "doing" {
			if mt, ok := mtimes[card.RepoRel()]; ok {
				item.Age = now.Sub(mt)
				item.Stale = staleAfter > 0 && item.Age >= staleAfter
			}
		}
		byZone[zone] = append(byZone[zone], item)
	}
	var zones []BoardZone
	seen := map[string]bool{}
	for _, name := range boardZoneOrder {
		if items, ok := byZone[name]; ok {
			zones = append(zones, BoardZone{Name: name, Tasks: items})
			seen[name] = true
		}
	}
	var others []string
	for name := range byZone {
		if !seen[name] {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	for _, name := range others {
		zones = append(zones, BoardZone{Name: name, Tasks: byZone[name]})
	}
	return zones
}

func (z BoardZone) openIssueCount() int {
	n := 0
	for _, item := range z.Tasks {
		if kindDir(item.Card.TasksRel) != "issue" {
			continue
		}
		if !item.Card.isResolvedIssue() && !item.Card.isBlockedIssue() {
			n++
		}
	}
	return n
}

func (z BoardZone) blockedIssueCount() int {
	n := 0
	for _, item := range z.Tasks {
		if item.Card.isBlockedIssue() {
			n++
		}
	}
	return n
}

func staleCount(zones []BoardZone) int {
	n := 0
	for _, zone := range zones {
		for _, item := range zone.Tasks {
			if item.Stale {
				n++
			}
		}
	}
	return n
}

// LoadBoardCards reads every card, including storage, and the mtimes of doing cards.
// A missing tasks directory is an empty board. A tasks path that exists but
// cannot be read is an error.
func LoadBoardCards(root string) ([]*Card, map[string]time.Time, error) {
	if err := readableTasksDir(root); err != nil {
		return nil, nil, err
	}
	cards, err := FindCards(root, false)
	if err != nil {
		return nil, nil, err
	}
	mtimes := map[string]time.Time{}
	for _, card := range cards {
		if BoardZoneOf(card) != "doing" || storageDir(card.TasksRel) != "" || kindDir(card.TasksRel) == "plan" {
			continue
		}
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(card.RepoRel())))
		if statErr == nil {
			mtimes[card.RepoRel()] = info.ModTime()
		}
	}
	return cards, mtimes, nil
}

func readableTasksDir(root string) error {
	path := filepath.Join(root, tasksDirName())
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open tasks tree: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("open tasks tree: %s is not a directory", tasksDirName())
	}
	if _, err := os.ReadDir(path); err != nil {
		return fmt.Errorf("open tasks tree: %w", err)
	}
	return nil
}

// RenderBoardText writes the claim board. now and staleAfter make age deterministic.
func RenderBoardText(w io.Writer, zones []BoardZone, staleHours int, plans PlanInventory) {
	fmt.Fprintln(w, "📋 Task Board")
	fmt.Fprintln(w)
	// A plans-only corpus has cards and no columns. The empty line is for a
	// board that holds neither.
	if len(zones) == 0 && plans.Total == 0 {
		fmt.Fprintln(w, "  (no tasks found under tasks/)")
		return
	}
	if len(zones) == 0 {
		writePlanInventoryText(w, plans)
		return
	}
	for _, zone := range zones {
		header := fmt.Sprintf("%s (%d)", zone.Name, len(zone.Tasks))
		if zone.Name == "issue" {
			if blocked := zone.blockedIssueCount(); blocked > 0 {
				header = fmt.Sprintf("%s (%d, %d open, %d blocked)", zone.Name, len(zone.Tasks), zone.openIssueCount(), blocked)
			} else {
				header = fmt.Sprintf("%s (%d, %d open)", zone.Name, len(zone.Tasks), zone.openIssueCount())
			}
		}
		if zone.Name == "doing" {
			header += "  ← claim board"
		}
		fmt.Fprintf(w, "  %s\n", header)
		for _, item := range zone.Tasks {
			title := item.Card.Title
			if title == "" {
				title = filepath.Base(item.Card.RepoRel())
			}
			marker := "  "
			if item.Stale {
				marker = "⚠ "
			}
			meta := ""
			if mod := ModuleLabel(item.Card, zone.Name); mod != "" {
				meta += " @" + mod
			}
			if item.Card.Priority != "" {
				meta += " [" + item.Card.Priority + "]"
			}
			if item.Card.isResolvedIssue() {
				meta += " ✓ resolved"
			}
			if item.Card.isBlockedIssue() {
				meta += " ⛔ blocked"
			}
			if zone.Name == "doing" && item.Age > 0 {
				meta += " claimed " + formatDuration(item.Age)
				if item.Stale {
					meta += fmt.Sprintf("  ⚠ STALE (>%dh)", staleHours)
				}
			}
			fmt.Fprintf(w, "    %s%-44s%s\n", marker, title, meta)
		}
		fmt.Fprintln(w)
	}
	if n := staleCount(zones); n > 0 {
		fmt.Fprintf(w, "⚠ %d stale claim(s) — a session may have died mid-work.\n", n)
		fmt.Fprintln(w, "  Recover intentionally: 'ce task move <file> todo'")
		fmt.Fprintln(w, "  (never auto-reclaim — a live session's in-progress work would be lost).")
	}
	writePlanInventoryText(w, plans)
}

// RenderBoardJSON writes the indented board document. An empty board has
// zones [] rather than null.
func RenderBoardJSON(w io.Writer, zones []BoardZone, staleHours int, plans PlanInventory) error {
	out := boardJSONOut{
		StaleHours: staleHours,
		StaleCount: staleCount(zones),
		Zones:      []boardJSONZone{},
		Plans:      plans.json(),
	}
	for _, zone := range zones {
		column := boardJSONZone{Name: zone.Name, Count: len(zone.Tasks), Tasks: []boardJSONTask{}}
		if zone.Name == "issue" {
			column.OpenCount = zone.openIssueCount()
			column.BlockedCount = zone.blockedIssueCount()
		}
		for _, item := range zone.Tasks {
			row := boardJSONTask{
				Path:     item.Card.RepoRel(),
				Title:    item.Card.Title,
				Priority: item.Card.Priority,
				Status:   string(item.Card.Status),
				Stale:    item.Stale,
				Resolved: item.Card.isResolvedIssue(),
				Blocked:  item.Card.isBlockedIssue(),
			}
			if mod := ModuleLabel(item.Card, zone.Name); mod != "" {
				row.Category = mod
			}
			if item.Age > 0 {
				row.AgeHours = item.Age.Hours()
			}
			column.Tasks = append(column.Tasks, row)
		}
		out.Zones = append(out.Zones, column)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

type boardJSONTask struct {
	Path     string  `json:"path"`
	Title    string  `json:"title"`
	Category string  `json:"category,omitempty"`
	Priority string  `json:"priority"`
	Status   string  `json:"status"`
	AgeHours float64 `json:"age_hours,omitempty"`
	Stale    bool    `json:"stale"`
	Resolved bool    `json:"resolved,omitempty"`
	Blocked  bool    `json:"blocked,omitempty"`
}

type boardJSONZone struct {
	Name         string          `json:"name"`
	Count        int             `json:"count"`
	OpenCount    int             `json:"open_count,omitempty"`
	BlockedCount int             `json:"blocked_count,omitempty"`
	Tasks        []boardJSONTask `json:"tasks"`
}

type boardJSONOut struct {
	StaleHours int             `json:"stale_hours"`
	StaleCount int             `json:"stale_count"`
	Zones      []boardJSONZone `json:"zones"`
	Plans      planJSON        `json:"plans"`
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

// ParseStaleHours accepts a non-negative whole number. Zero disables staleness.
func ParseStaleHours(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid --stale-hours: %q", raw)
	}
	return value, nil
}
