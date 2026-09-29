package taskflow

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
)

// ListCards reads the live board in walk order.
func ListCards(root string) ([]*Card, error) {
	return FindCards(root, true)
}

// taskJSON is the CE list payload. Field order is the byte contract.
type taskJSON struct {
	Path               string   `json:"path"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	Priority           string   `json:"priority"`
	Effort             string   `json:"effort"`
	Category           string   `json:"category"`
	Created            string   `json:"created"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

func toTaskJSON(card *Card) taskJSON {
	criteria := []string{}
	for _, c := range card.Criteria() {
		criteria = append(criteria, c.Text)
	}
	return taskJSON{
		Path:               card.RepoRel(),
		Title:              card.Title,
		Status:             string(card.Status),
		Priority:           card.Priority,
		Effort:             card.Effort,
		Category:           card.Category,
		Created:            card.Created,
		AcceptanceCriteria: criteria,
	}
}

// ListJSON writes the whole board as CE's `task list --json` payload. The
// task noun deliberately bypasses outputformat.Encode: the CE contract bytes
// are bare 2-space pretty JSON, with no outputVersion splice around them.
func ListJSON(w io.Writer, root string) error {
	cards, err := ListCards(root)
	if err != nil {
		return err
	}
	payload := struct {
		Directory string     `json:"directory"`
		Total     int        `json:"total"`
		Tasks     []taskJSON `json:"tasks"`
	}{
		Directory: filepath.Join(root, TasksDir),
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

// ListText writes CE's `task list` board rendering.
func ListText(w io.Writer, root string) error {
	cards, err := ListCards(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "=== Task List ===\n")
	fmt.Fprintf(w, "Directory: %s\n", filepath.Join(root, TasksDir))
	fmt.Fprintf(w, "\n")
	for _, card := range cards {
		fmt.Fprintf(w, "%s %s\n", card.Status.Symbol(), card.RepoRel())
		fmt.Fprintf(w, "   %s | %s | %s\n", card.Title, card.Priority, card.Effort)
	}
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "Total: %d task(s)\n", len(cards))
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "=== End ===\n")
	return nil
}
