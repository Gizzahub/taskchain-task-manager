package taskflow

import "strings"

// applySequenceFields copies the list-shaped frontmatter the lenient reader
// otherwise drops. scalarString cannot represent a sequence, and these
// commands need the sequences without a second card parser.
func applySequenceFields(card *Card, doc map[string]any) {
	if doc == nil {
		return
	}
	card.DependsOn = mergeLists(stringList(doc["depends-on"]), stringList(doc["dependencies"]))
	card.Children = stringList(doc["children"])
	card.Blocks = stringList(doc["blocks"])
	card.PromotedTo = stringList(doc["promoted-to"])
	card.BlockedBy = stringList(doc["blocked-by"])
}

func mergeLists(primary, extra []string) []string {
	if len(extra) == 0 {
		return primary
	}
	seen := map[string]bool{}
	out := append([]string{}, primary...)
	for _, item := range out {
		seen[item] = true
	}
	for _, item := range extra {
		if !seen[item] {
			out = append(out, item)
			seen[item] = true
		}
	}
	return out
}

// stringList accepts a scalar or a sequence. Anything else is empty: a
// malformed shape is not a reason to refuse the rest of the board.
func stringList(value any) []string {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{strings.TrimSpace(v)}
	case []any:
		var out []string
		for _, item := range v {
			text := strings.TrimSpace(scalarString(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// Score is the recommendation weight: priority first, then a small effort bias.
func (c *Card) Score() int {
	if c == nil {
		return 0
	}
	score := 0
	switch c.Priority {
	case "High":
		score = 100
	case "Medium":
		score = 50
	case "Low":
		score = 10
	}
	switch c.Effort {
	case "XS":
		score += 5
	case "S":
		score += 3
	case "L":
		score -= 2
	case "XL":
		score -= 5
	}
	return score
}

// IssueState derives open|blocked|promoted|resolved for an issue card.
// A nil unfinished function treats every promoted-to id as unfinished.
// Non-issue cards return "".
func (c *Card) IssueState(unfinished func(id string) bool) string {
	if c == nil || kindDir(c.TasksRel) != "issue" {
		return ""
	}
	if c.Resolution != "" {
		return "resolved"
	}
	if c.Disposition == "blocked" {
		return "blocked"
	}
	for _, id := range c.PromotedTo {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if unfinished == nil || unfinished(id) {
			return "promoted"
		}
	}
	return "open"
}

func (c *Card) isResolvedIssue() bool {
	return c != nil && kindDir(c.TasksRel) == "issue" && c.Resolution != ""
}

func (c *Card) isBlockedIssue() bool {
	return c != nil && kindDir(c.TasksRel) == "issue" && c.Disposition == "blocked"
}

// kindDir is the kind segment of a tasks-relative path, ignoring the file name.
func kindDir(tasksRel string) string {
	parts := strings.Split(tasksRel, "/")
	for i := 0; i < len(parts)-1; i++ {
		if IsKindDir(parts[i]) {
			return parts[i]
		}
	}
	return ""
}

func storageDir(tasksRel string) string {
	parts := strings.Split(tasksRel, "/")
	for i := 0; i < len(parts)-1; i++ {
		if IsStorageDir(parts[i]) {
			return parts[i]
		}
	}
	return ""
}

// sectionBody returns the trimmed prose under one ## heading, without a
// following heading. An HTML comment alone is not a description.
func sectionBody(body, heading string) string {
	lines := strings.Split(body, "\n")
	start := -1
	want := "## " + heading
	for i, line := range lines {
		if strings.TrimSpace(line) == want {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	var kept []string
	for _, line := range lines[start:] {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			break
		}
		kept = append(kept, line)
	}
	text := strings.TrimSpace(strings.Join(kept, "\n"))
	if text == "" || strings.HasPrefix(text, "<!--") && strings.HasSuffix(text, "-->") {
		return ""
	}
	return text
}

func (c *Card) description() string {
	if c == nil {
		return ""
	}
	if text := sectionBody(c.Body, "Summary"); text != "" {
		return text
	}
	return sectionBody(c.Body, "Description")
}
