package taskflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// resolveNow is the clock ResolveIssue stamps into resolved-at. Tests freeze
// it so a same-second rewrite is the only identical-bytes no-op.
var resolveNow = time.Now

// ResolveResult describes one issue resolution.
type ResolveResult struct {
	Path       string
	Resolution string
	By         string
	ResolvedAt string
	Summary    string
	Changed    bool
}

// ResolveIssue stamps resolution, resolved-at, and resolution-summary in place.
// by is optional; a blank value means the flag was absent.
func ResolveIssue(ctx context.Context, root, cardPath, resolution, by string) (*ResolveResult, error) {
	return resolveIssue(ctx, root, cardPath, resolution, by, strings.TrimSpace(by) != "")
}

// ResolveIssueWithByFlag records whether --by was present, including an
// explicit empty value. wontfix and invalid reject that presence; fixed and
// duplicate reject an empty value as input.
func ResolveIssueWithByFlag(ctx context.Context, root, cardPath, resolution, by string, byProvided bool) (*ResolveResult, error) {
	return resolveIssue(ctx, root, cardPath, resolution, by, byProvided)
}

func resolveIssue(ctx context.Context, root, cardPath, resolution, by string, byProvided bool) (*ResolveResult, error) {
	_ = ctx
	by = strings.TrimSpace(by)
	if resolution != "fixed" && resolution != "wontfix" && resolution != "duplicate" && resolution != "invalid" {
		return nil, fmt.Errorf("invalid resolution %q (allowed: fixed, wontfix, duplicate, invalid)", resolution)
	}
	if _, err := ResolveTasksDir(); err != nil {
		return nil, err
	}
	statPath := cardPath
	if !filepath.IsAbs(cardPath) {
		statPath = filepath.Join(root, cardPath)
	}
	info, err := os.Lstat(statPath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", cardPath)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// The card writer follows a symlink with WriteFile. Refuse one whose
		// target leaves the corpus so a link cannot relocate the stamp.
		resolved, err := filepath.EvalSymlinks(statPath)
		if err != nil {
			return nil, fmt.Errorf("file not found: %s", cardPath)
		}
		corpus, err := filepath.EvalSymlinks(filepath.Join(root, tasksDirName()))
		if err != nil {
			return nil, fmt.Errorf("%s is not under issue/; ce task resolve only closes issue cards", cardPath)
		}
		rel, err := filepath.Rel(corpus, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s is not under issue/; ce task resolve only closes issue cards", cardPath)
		}
	}
	rel, ok := relUnderTasks(cardPath)
	if !ok || kindDir(rel) != "issue" {
		return nil, fmt.Errorf("%s is not under issue/; ce task resolve only closes issue cards", cardPath)
	}
	full := filepath.Join(root, tasksDirName(), filepath.FromSlash(rel))
	if byProvided && (resolution == "wontfix" || resolution == "invalid") {
		return nil, fmt.Errorf("--by is not accepted with %s; it records no card", resolution)
	}
	if byProvided && by == "" {
		if resolution == "duplicate" {
			return nil, fmt.Errorf("--by needs an ISSUE id")
		}
		return nil, fmt.Errorf("--by needs a TASK id")
	}
	if byProvided {
		cards, findErr := FindCards(root, false)
		if findErr != nil {
			return nil, fmt.Errorf("cannot verify --by target: %w", findErr)
		}
		wantKind := "task"
		if resolution == "duplicate" {
			wantKind = "issue"
		}
		if err := verifyResolveBy(cards, by, wantKind); err != nil {
			return nil, err
		}
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", cardPath)
	}
	if frontmatterMalformed(raw) {
		return nil, fmt.Errorf("malformed frontmatter: %s", cardPath)
	}
	at := resolveNow().UTC().Format(time.RFC3339)
	summary := fmt.Sprintf("Resolved as %s.", resolution)
	if by != "" {
		summary = fmt.Sprintf("Resolved as %s by %s.", resolution, by)
	}
	stamped, changed := stampResolutionFields(raw, [][2]string{
		{"resolution", resolution},
		{"resolved-at", at},
		{"resolution-summary", quoteResolutionSummary(summary)},
	})
	if !changed {
		return &ResolveResult{Path: cardPath, Resolution: resolution, By: by, ResolvedAt: at, Summary: summary, Changed: false}, nil
	}
	if err := writePreservingMode(full, stamped); err != nil {
		return nil, err
	}
	return &ResolveResult{Path: cardPath, Resolution: resolution, By: by, ResolvedAt: at, Summary: summary, Changed: true}, nil
}

func frontmatterMalformed(raw []byte) bool {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	_, _, detected, err := parseCanonicalDocument(text)
	return !detected || err != nil
}

func verifyResolveBy(cards []*Card, by, wantKind string) error {
	claims := map[string]int{}
	for _, card := range cards {
		if card == nil {
			continue
		}
		name := filepath.Base(card.RepoRel())
		if safeFileName.MatchString(name) {
			claims[name]++
		}
	}
	var known cardIdentifiers
	exactIDs := 0
	exactStems := 0
	token := strings.TrimSpace(by)
	stemToken := strings.TrimSuffix(token, ".md")
	normToken := normalizeCardID(token)
	for _, card := range cards {
		if card == nil || cardKindForResolve(card) != wantKind {
			continue
		}
		id := strings.TrimSpace(card.ID)
		if id != "" && (id == token || (normToken != "" && normalizeCardID(id) == normToken)) {
			exactIDs++
		}
		stem := strings.TrimSuffix(filepath.Base(card.RepoRel()), ".md")
		if !strings.HasPrefix(token, "file:") && stem == stemToken {
			exactStems++
		}
		known.add(stem)
		known.add(id)
		name := filepath.Base(card.RepoRel())
		if id == "" && claims[name] == 1 && closedFrontmatter(card.Raw) {
			known.addFileIdentifier(name)
		}
	}
	if exactIDs > 1 || exactStems > 1 {
		return fmt.Errorf("--by names %q, which is ambiguous in this corpus", by)
	}
	if known.matches(token) {
		return nil
	}
	if wantKind == "issue" {
		return fmt.Errorf("--by names %q, which is not an ISSUE card; duplicate points at the original ISSUE card", by)
	}
	return fmt.Errorf("--by names %q, which is not a TASK card; fixed is resolved by a TASK card", by)
}

func cardKindForResolve(card *Card) string {
	if card == nil {
		return ""
	}
	if kind, ok := canonicalKindFromID(strings.TrimSpace(card.ID)); ok {
		return string(kind)
	}
	if kind, ok := canonicalKindFromPath(card.TasksRel); ok {
		return string(kind)
	}
	return ""
}

// quoteResolutionSummary matches the reference resolver: resolution-summary is
// free text and reaches the line stamp already quoted.
func quoteResolutionSummary(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

type resolutionLine struct {
	text string
	end  string
}

func splitResolutionLines(content []byte) []resolutionLine {
	rest := string(content)
	var lines []resolutionLine
	for len(rest) > 0 {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			lines = append(lines, resolutionLine{text: rest})
			break
		}
		text := rest[:i]
		end := "\n"
		if strings.HasSuffix(text, "\r") {
			text = strings.TrimSuffix(text, "\r")
			end = "\r\n"
		}
		lines = append(lines, resolutionLine{text: text, end: end})
		rest = rest[i+1:]
	}
	return lines
}

func joinResolutionLines(lines []resolutionLine) []byte {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line.text)
		b.WriteString(line.end)
	}
	return []byte(b.String())
}

// stampResolutionFields replaces existing keys in place and inserts missing
// keys before the closing fence. The shared stamp helper always writes LF, so
// a CRLF card would lose its line ending on every stamped line; this copy
// keeps each line's ending and uses the fence's ending for insertions.
// Unclosed or missing fences return the original bytes and changed=false
// without inventing a write. Callers refuse that shape before stamping.
func stampResolutionFields(content []byte, pairs [][2]string) ([]byte, bool) {
	lines := splitResolutionLines(content)
	if len(lines) == 0 || lines[0].text != "---" {
		return content, false
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i].text == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return content, false
	}
	nl := lines[closing].end
	if nl == "" {
		nl = "\n"
	}
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		prefix := key + ":"
		replaced := false
		for i := 1; i < closing; i++ {
			if strings.HasPrefix(lines[i].text, prefix) {
				lines[i].text = key + ": " + value
				if lines[i].end == "" {
					lines[i].end = nl
				}
				replaced = true
				break
			}
		}
		if replaced {
			continue
		}
		entry := resolutionLine{text: key + ": " + value, end: nl}
		updated := append([]resolutionLine{}, lines[:closing]...)
		updated = append(updated, entry)
		updated = append(updated, lines[closing:]...)
		lines = updated
		closing++
	}
	rewritten := joinResolutionLines(lines)
	if string(rewritten) == string(content) {
		return content, false
	}
	return rewritten, true
}
