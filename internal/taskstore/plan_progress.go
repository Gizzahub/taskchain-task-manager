package taskstore

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// PlanProgress is a plan card's derived child rollup. It is computed from the
// census of declared children, never authored as an independent claim.
type PlanProgress struct {
	Total             int
	Completed         int
	Progress          int
	CompletedChildren int
}

// PlanProgressFields are the optional plan-card counters rewritten in place.
// Both completed-tasks and completed-children hold the child completion count
// today; both are maintained so consumers that read either stay consistent.
func PlanProgressFields() []string {
	return []string{"total-tasks", "completed-tasks", "completed-children", "progress"}
}

// NewPlanProgress derives the percentage from a child census. An empty plan
// reports 0%, not 100%: no children means work has not started.
func NewPlanProgress(total, completed int) PlanProgress {
	if total < 0 {
		total = 0
	}
	if completed < 0 {
		completed = 0
	}
	if completed > total {
		completed = total
	}
	progress := 0
	if total > 0 {
		progress = completed * 100 / total
	}
	return PlanProgress{
		Total:             total,
		Completed:         completed,
		Progress:          progress,
		CompletedChildren: completed,
	}
}

// Fields renders counters as ordered key/value pairs for a frontmatter stamp.
func (p PlanProgress) Fields() [][2]string {
	values := map[string]int{
		"total-tasks":        p.Total,
		"completed-tasks":    p.Completed,
		"completed-children": p.CompletedChildren,
		"progress":           p.Progress,
	}
	names := PlanProgressFields()
	out := make([][2]string, 0, len(names))
	for _, name := range names {
		out = append(out, [2]string{name, strconv.Itoa(values[name])})
	}
	return out
}

// StampPlanProgressBytes rewrites the four derived counters inside existing
// frontmatter without a YAML round-trip. Missing keys are appended before the
// closing fence. Unterminated or absent frontmatter is left unchanged.
func StampPlanProgressBytes(raw []byte, progress PlanProgress) ([]byte, bool, error) {
	lines := bytes.SplitAfter(raw, []byte("\n"))
	if len(lines) == 0 || strings.TrimRight(string(lines[0]), "\r\n") != "---" {
		return append([]byte(nil), raw...), false, nil
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(string(lines[i]), "\r\n") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return append([]byte(nil), raw...), false, nil
	}
	for _, pair := range progress.Fields() {
		key, value := pair[0], pair[1]
		prefix := []byte(key + ":")
		nl := "\n"
		if bytes.HasSuffix(lines[0], []byte("\r\n")) || (closing < len(lines) && bytes.HasSuffix(lines[closing], []byte("\r\n"))) {
			nl = "\r\n"
		}
		entry := []byte(key + ": " + value + nl)
		replaced := false
		for i := 1; i < closing; i++ {
			line := lines[i]
			if bytes.HasPrefix(line, prefix) || bytes.HasPrefix(bytes.TrimLeft(line, " \t"), prefix) {
				indent := line[:len(line)-len(bytes.TrimLeft(line, " \t"))]
				lines[i] = append(append([]byte{}, indent...), entry...)
				replaced = true
				break
			}
		}
		if replaced {
			continue
		}
		updated := append([][]byte{}, lines[:closing]...)
		updated = append(updated, entry)
		updated = append(updated, lines[closing:]...)
		lines = updated
		closing++
	}
	out := bytes.Join(lines, nil)
	if bytes.Equal(out, raw) {
		return append([]byte(nil), raw...), false, nil
	}
	if !bytes.HasPrefix(out, []byte("---")) {
		return nil, false, fmt.Errorf("plan progress stamp corrupted frontmatter")
	}
	return out, true, nil
}
