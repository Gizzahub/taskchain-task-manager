package taskflow

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TasksDir is the board root the task noun reads by default. TASKS_DIR
// redirects the whole noun at another corpus directory.
const TasksDir = "tasks"

// ResolveTasksDir reads TASKS_DIR from the environment, defaulting to
// "tasks", and is the one place every task command asks the question, so an
// invalid value is caught once rather than at each call site. An absolute
// override is refused rather than passed through: joining an absolute second
// argument onto the working directory silently names a directory nothing
// populated, and every command would report an empty board instead of the
// misconfiguration.
func ResolveTasksDir() (string, error) {
	tasksDir := os.Getenv("TASKS_DIR")
	if tasksDir == "" {
		return TasksDir, nil
	}
	if filepath.IsAbs(tasksDir) {
		return "", fmt.Errorf("TASKS_DIR must be a directory name relative to the working directory, not an absolute path: %s", tasksDir)
	}
	return tasksDir, nil
}

// tasksDirName is the resolved spelling the engine's readers use. Commands
// refuse an absolute override at dispatch; the fallback here only keeps a
// library caller reading the default board.
func tasksDirName() string {
	name, err := ResolveTasksDir()
	if err != nil {
		return TasksDir
	}
	return name
}

// Card is the lenient view of one task file. A field a malformed card cannot
// supply is simply empty: the reader never refuses a board CE could still see.
type Card struct {
	// RelPath is the card's path relative to the repo root, e.g.
	// tasks/todo/001-draft.md.
	RelPath string
	// TasksRel is the path relative to the tasks directory.
	TasksRel string
	// Zone is the canonical zone word the card's directory encodes, when any.
	Zone string
	// Status is the effective status: the zone segment wins over frontmatter.
	Status Status
	// FMStatus is the frontmatter status word before zone resolution.
	FMStatus string
	ID       string
	Title    string
	Type     string
	Priority string
	Effort   string
	Category string
	Created  string
	// Parent is the parent plan id, when the frontmatter names one.
	Parent string
	// Frontmatter keeps the raw scalar fields for validators.
	Frontmatter map[string]string
	// Body is everything after the closing frontmatter fence.
	Body string
	// Raw is the exact file bytes.
	Raw []byte
}

// RepoRel returns the tasks-prefixed spelling ("tasks/todo/001-draft.md").
func (c *Card) RepoRel() string { return pathJoin(tasksDirName(), c.TasksRel) }

// Criteria are the graded checkbox lines of the card body.
func (c *Card) Criteria() []Criterion {
	return criterionLines(c.Body)
}

func pathJoin(a, b string) string {
	if a == "" {
		return b
	}
	return a + "/" + b
}

// FindCards walks the default tasks tree lexically and reads every card it
// holds, excluding storage when excludeArchive is set. README/INDEX/TEMPLATE
// files and .ce/evidence subtrees are not cards.
func FindCards(root string, excludeArchive bool) ([]*Card, error) {
	return FindCardsIn(root, tasksDirName(), excludeArchive)
}

// FindCardsIn walks the board rooted at tasksDir — the directory TASKS_DIR
// redirects the walk at — with the same card exclusions as FindCards.
func FindCardsIn(root, tasksDir string, excludeArchive bool) ([]*Card, error) {
	tasksPath := filepath.Join(root, tasksDir)
	var cards []*Card
	err := filepath.WalkDir(tasksPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are not cards
		}
		relToTasks, relErr := filepath.Rel(tasksPath, p)
		if relErr != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p == tasksPath {
				return nil
			}
			if name == ".ce" || name == "evidence" {
				return filepath.SkipDir
			}
			if excludeArchive && IsStorageDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".md" {
			return nil
		}
		switch strings.ToLower(name) {
		case "readme.md", "index.md", "template.md":
			return nil
		}
		card, readErr := ReadCardIn(root, tasksDir, filepath.ToSlash(relToTasks))
		if readErr != nil || card == nil {
			return nil
		}
		cards = append(cards, card)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return cards, err
	}
	return cards, nil
}

// ReadCard reads one card given its path relative to the default tasks
// directory. A card outside any zone keeps its frontmatter status.
func ReadCard(root, tasksRel string) (*Card, error) {
	return ReadCardIn(root, tasksDirName(), tasksRel)
}

// ReadCardIn reads one card relative to the board rooted at tasksDir.
func ReadCardIn(root, tasksDir, tasksRel string) (*Card, error) {
	full := filepath.Join(root, tasksDir, filepath.FromSlash(tasksRel))
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	card := ParseCard(filepath.ToSlash(tasksRel), raw)
	return card, nil
}

// ParseCard applies the lenient reader to raw bytes. Zone segments override
// the frontmatter status after it is applied, which is the CE order.
func ParseCard(tasksRel string, raw []byte) *Card {
	card := &Card{
		TasksRel:    tasksRel,
		Frontmatter: map[string]string{},
		Raw:         raw,
	}
	fm, body := splitFrontmatter(raw)
	card.Body = body
	if fm != nil {
		var doc map[string]any
		_ = yaml.Unmarshal(fm, &doc)
		for key, value := range doc {
			card.Frontmatter[key] = scalarString(value)
		}
		card.ID = card.Frontmatter["id"]
		card.Title = card.Frontmatter["title"]
		card.Type = card.Frontmatter["type"]
		card.Priority = parseTaskPriority(card.Frontmatter["priority"])
		card.Effort = parseTaskEffort(card.Frontmatter["effort"])
		card.FMStatus = strings.TrimSpace(card.Frontmatter["status"])
		card.Parent = card.Frontmatter["parent"]
		card.Created = fmDate(card.Frontmatter["created"])
	}
	parts := strings.Split(tasksRel, "/")
	if len(parts) > 1 {
		card.Category = parts[0]
	}
	if _, zoneStatus, ok := ZoneSegment(parts); ok {
		card.Zone = zoneStatus.Dir()
		card.Status = zoneStatus
	} else if st, ok := StatusFromWord(card.FMStatus); ok {
		// No zone segment: kind directories and storage keep the frontmatter
		// word, resolved through the status vocabulary when it recognises it.
		card.Status = st
	}
	return card
}

// splitFrontmatter splits raw into frontmatter bytes (without fences) and the
// body. A file with no closing fence has no frontmatter at all: guessing where
// an unterminated block ends would read body prose as schema.
func splitFrontmatter(raw []byte) ([]byte, string) {
	text := string(raw)
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		if rest, ok = strings.CutPrefix(text, "---\r\n"); !ok {
			return nil, text
		}
	}
	offset := 0
	for {
		nl := strings.IndexByte(rest[offset:], '\n')
		if nl < 0 {
			return nil, text
		}
		lineEnd := offset + nl
		if strings.TrimRight(rest[offset:lineEnd], "\r") == "---" {
			bodyStart := lineEnd + 1
			if bodyStart > len(rest) {
				bodyStart = len(rest)
			}
			return []byte(rest[:offset]), rest[bodyStart:]
		}
		offset = lineEnd + 1
	}
}

func scalarString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	case time.Time:
		return v.Format("2006-01-02")
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func fmDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

// parseTaskPriority maps frontmatter priority spellings to the display label.
func parseTaskPriority(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "p0"), strings.Contains(lower, "p1"), strings.Contains(lower, "critical"):
		return "High"
	case strings.Contains(lower, "p2"):
		return "Medium"
	case strings.Contains(lower, "p3"):
		return "Low"
	}
	switch lower {
	case "high", "urgent":
		return "High"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	}
	return "Medium"
}

// parseTaskEffort maps frontmatter effort spellings to the display token.
func parseTaskEffort(raw string) string {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	for _, token := range []string{"XS", "XL", "S", "M", "L"} {
		if strings.Contains(upper, token) {
			return token
		}
	}
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "day"):
		return "XL"
	case strings.Contains(lower, "hour"):
		return "L"
	case strings.Contains(lower, "min"):
		return "S"
	}
	return "M"
}

// TasksDirName is the resolved board-root spelling for callers outside the
// engine. Commands refuse an absolute TASKS_DIR at dispatch, so the fallback
// here only covers a library caller that skipped that guard.
func TasksDirName() string { return tasksDirName() }
