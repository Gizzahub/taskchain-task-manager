package taskflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrPromoteInput marks a refusal caused by what the caller asked for.
var ErrPromoteInput = errors.New("promote: invalid input")

// PromoteOptions selects issues. AllOpen sweeps every open issue. DryRun
// writes nothing and allocates no id.
type PromoteOptions struct {
	Targets []string
	AllOpen bool
	DryRun  bool
	Created string
}

// Promotion is one issue's outcome.
type Promotion struct {
	IssuePath string
	IssueID   string
	TaskID    string
	TaskPath  string
	Skipped   bool
	Reason    string
}

// PromoteResult is one invocation. Promoted and Skipped are always present.
type PromoteResult struct {
	DryRun     bool
	Candidates []Promotion
	Promoted   []Promotion
	Skipped    []Promotion
}

var numericCardPrefix = regexp.MustCompile(`^[A-Za-z]*-?\d+-`)

// Promote turns open issues into TASK cards and stamps promoted-to.
// A later failure deletes every TASK file this call created and leaves
// earlier promoted-to stamps in place. Reserved numbers are not returned.
func Promote(ctx context.Context, root string, opts PromoteOptions) (*PromoteResult, error) {
	if err := readableTasksDir(root); err != nil {
		return nil, err
	}
	cards, err := LoadLiveCards(root)
	if err != nil {
		return nil, err
	}
	all, findErr := FindCards(root, false)
	if findErr != nil {
		return nil, findErr
	}
	finished := buildCompletionIndex(all)
	candidates, result, err := promoteCandidates(cards, finished, opts)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		result.DryRun = true
		for _, candidate := range candidates {
			result.Candidates = append(result.Candidates, Promotion{
				IssuePath: candidate.RepoRel(),
				IssueID:   displayID(candidate),
			})
		}
		return result, nil
	}

	var written []string
	rollback := func() {
		for i := len(written) - 1; i >= 0; i-- {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(written[i])))
		}
	}
	for _, candidate := range candidates {
		promotion, createErr := promoteOne(ctx, root, candidate, opts)
		if createErr != nil {
			rollback()
			return nil, createErr
		}
		written = append(written, promotion.TaskPath)
		if stampErr := stampPromotedTo(root, candidate.RepoRel(), promotion.TaskID); stampErr != nil {
			rollback()
			return nil, stampErr
		}
		result.Promoted = append(result.Promoted, *promotion)
	}
	return result, nil
}

func promoteCandidates(cards []*Card, finished *completionIndex, opts PromoteOptions) ([]*Card, *PromoteResult, error) {
	result := &PromoteResult{Promoted: []Promotion{}, Skipped: []Promotion{}}
	states := issueStates(cards, finished)
	var selected []*Card
	if opts.AllOpen {
		for _, card := range cards {
			if kindDir(card.TasksRel) == "issue" {
				selected = append(selected, card)
			}
		}
		sort.Slice(selected, func(i, j int) bool { return selected[i].RepoRel() < selected[j].RepoRel() })
	} else {
		index := buildCardIndex(cards)
		for _, target := range opts.Targets {
			card, err := index.resolve(target)
			if err != nil {
				return nil, nil, err
			}
			selected = append(selected, card)
		}
	}

	duplicates := map[string]int{}
	if opts.AllOpen {
		for _, card := range selected {
			if id := strings.TrimSpace(card.ID); id != "" {
				duplicates[id]++
			}
		}
	}
	var candidates []*Card
	for _, card := range selected {
		if kindDir(card.TasksRel) != "issue" {
			return nil, nil, fmt.Errorf("%w: %s is not under issue/; ce task promote promotes issue cards", ErrPromoteInput, card.RepoRel())
		}
		id := strings.TrimSpace(card.ID)
		if opts.AllOpen && id != "" && duplicates[id] > 1 {
			result.Skipped = append(result.Skipped, Promotion{
				IssuePath: card.RepoRel(), IssueID: id, Skipped: true,
				Reason: fmt.Sprintf("duplicate issue ID %s appears in %d cards; ambiguous sources are never promoted", id, duplicates[id]),
			})
			continue
		}
		if reason := skipReason(card, states[card.RepoRel()]); reason != "" {
			issueID := id
			if issueID == "" {
				issueID = displayID(card)
			}
			result.Skipped = append(result.Skipped, Promotion{
				IssuePath: card.RepoRel(), IssueID: issueID, Skipped: true, Reason: reason,
			})
			continue
		}
		candidates = append(candidates, card)
	}
	return candidates, result, nil
}

func issueStates(cards []*Card, finished *completionIndex) map[string]string {
	states := map[string]string{}
	for _, card := range cards {
		if kindDir(card.TasksRel) != "issue" {
			continue
		}
		if state := card.IssueState(finished.unfinished); state != "" {
			states[card.RepoRel()] = state
		}
	}
	return states
}

func skipReason(card *Card, state string) string {
	if state == "resolved" || strings.TrimSpace(card.Resolution) != "" {
		return "resolved as " + strings.TrimSpace(card.Resolution) + "; a closed issue has nothing left to promote"
	}
	if disposition := strings.TrimSpace(card.Disposition); state == "blocked" || disposition != "" {
		return "disposition " + disposition + "; only open issues can be promoted"
	}
	if len(card.PromotedTo) > 0 {
		return "already promoted to " + strings.Join(card.PromotedTo, ", ") + "; an existing link is never overwritten"
	}
	return ""
}

func promoteOne(ctx context.Context, root string, card *Card, opts PromoteOptions) (*Promotion, error) {
	issueID := displayID(card)
	created, err := Create(ctx, NewCardOptions{
		Kind:    "task",
		Title:   promotedTitle(card),
		Slug:    promotedSlug(card.RepoRel()),
		Created: opts.Created,
		Sources: []string{fmt.Sprintf("%s — %s", issueID, card.RepoRel())},
		Root:    root,
	})
	if err != nil {
		return nil, fmt.Errorf("create the TASK for %s: %w", issueID, err)
	}
	return &Promotion{
		IssuePath: card.RepoRel(),
		IssueID:   issueID,
		TaskID:    created.ID,
		TaskPath:  created.Path,
	}, nil
}

func displayID(card *Card) string {
	if id := strings.TrimSpace(card.ID); id != "" {
		return id
	}
	return strings.TrimSuffix(filepath.Base(card.RepoRel()), ".md")
}

func promotedTitle(card *Card) string {
	if title := strings.TrimSpace(card.Title); title != "" {
		return title
	}
	return strings.TrimSuffix(filepath.Base(card.RepoRel()), ".md")
}

func promotedSlug(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	slug := numericCardPrefix.ReplaceAllString(stem, "")
	if !slugRe.MatchString(slug) || !slugLetterRe.MatchString(slug) {
		return ""
	}
	return slug
}

type cardIndex struct {
	cards []*Card
	byID  map[string][]*Card
}

func buildCardIndex(cards []*Card) *cardIndex {
	index := &cardIndex{cards: cards, byID: map[string][]*Card{}}
	for _, card := range cards {
		if card == nil {
			continue
		}
		if id := strings.TrimSpace(card.ID); id != "" {
			key := strings.ToUpper(id)
			index.byID[key] = append(index.byID[key], card)
			if norm := normalizeCardID(key); norm != "" && norm != key {
				index.byID[norm] = append(index.byID[norm], card)
			}
		}
	}
	return index
}

func (index *cardIndex) resolve(target string) (*Card, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("%w: empty target", ErrPromoteInput)
	}
	key := strings.ToUpper(strings.TrimSuffix(target, ".md"))
	matches := index.byID[key]
	if len(matches) == 0 {
		if norm := normalizeCardID(key); norm != "" {
			matches = index.byID[norm]
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		return nil, fmt.Errorf("%w: %q names %d cards (%s); give the path", ErrPromoteInput, target, len(matches), joinCardPaths(matches))
	}
	wanted := filepath.ToSlash(filepath.Clean(target))
	byBasename := !strings.Contains(wanted, "/")
	var byPath []*Card
	for _, card := range index.cards {
		slashed := card.RepoRel()
		if slashed == wanted || (byBasename && filepath.Base(slashed) == wanted) {
			byPath = append(byPath, card)
		}
	}
	switch len(byPath) {
	case 1:
		return byPath[0], nil
	case 0:
		return nil, fmt.Errorf("%w: no card in this corpus answers to %q", ErrPromoteInput, target)
	default:
		return nil, fmt.Errorf("%w: %q names %d cards (%s); give the path", ErrPromoteInput, target, len(byPath), joinCardPaths(byPath))
	}
}

func joinCardPaths(cards []*Card) string {
	paths := make([]string, 0, len(cards))
	for _, card := range cards {
		paths = append(paths, card.RepoRel())
	}
	return strings.Join(paths, ", ")
}

// stampPromotedTo inserts `promoted-to: [ID]` before the closing fence.
// A card with no frontmatter fails closed so the caller can delete the TASK.
func stampPromotedTo(root, repoRel, taskID string) error {
	full := filepath.Join(root, filepath.FromSlash(repoRel))
	raw, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("stamp promoted-to on %s: %w", repoRel, err)
	}
	fence := closingFenceOffset(raw)
	if fence < 0 {
		return fmt.Errorf("stamp promoted-to on %s: card has no frontmatter", repoRel)
	}
	nl := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		nl = "\r\n"
	}
	var buf bytes.Buffer
	buf.Write(raw[:fence])
	if fence > 0 && raw[fence-1] != '\n' {
		buf.WriteString(nl)
	}
	buf.WriteString("promoted-to: [" + taskID + "]" + nl)
	buf.Write(raw[fence:])
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("stamp promoted-to on %s: %w", repoRel, err)
	}
	return nil
}

func closingFenceOffset(raw []byte) int {
	text := string(raw)
	rest, ok := strings.CutPrefix(text, "---\n")
	prefix := 4
	if !ok {
		rest, ok = strings.CutPrefix(text, "---\r\n")
		prefix = 5
		if !ok {
			return -1
		}
	}
	offset := 0
	for {
		nl := strings.IndexByte(rest[offset:], '\n')
		if nl < 0 {
			return -1
		}
		lineEnd := offset + nl
		if strings.TrimRight(rest[offset:lineEnd], "\r") == "---" {
			return prefix + offset
		}
		offset = lineEnd + 1
	}
}

// RenderPromoteText writes candidates, then promotions, then skips.
func RenderPromoteText(w io.Writer, result *PromoteResult) {
	if result == nil {
		result = &PromoteResult{}
	}
	for _, candidate := range result.Candidates {
		fmt.Fprintf(w, "📋 %s would be promoted (%s)\n", candidate.IssueID, candidate.IssuePath)
	}
	for _, promotion := range result.Promoted {
		fmt.Fprintf(w, "✅ %s → %s\n", promotion.IssueID, promotion.TaskID)
		fmt.Fprintf(w, "   %s\n", promotion.TaskPath)
	}
	for _, skipped := range result.Skipped {
		fmt.Fprintf(w, "⏭️  %s skipped: %s\n", skipped.IssueID, skipped.Reason)
	}
	if len(result.Promoted) == 0 && len(result.Candidates) == 0 {
		if result.DryRun {
			fmt.Fprintln(w, "No issue would be promoted.")
		} else {
			fmt.Fprintln(w, "No issue was promoted.")
		}
	}
}

// RenderPromoteJSON writes the indented promote document.
func RenderPromoteJSON(w io.Writer, result *PromoteResult) error {
	if result == nil {
		result = &PromoteResult{}
	}
	payload := promoteJSON{
		DryRun:   result.DryRun,
		Promoted: []promotionJSON{},
		Skipped:  []promotionJSON{},
	}
	for _, candidate := range result.Candidates {
		payload.Candidates = append(payload.Candidates, promotionJSON{
			Issue: candidate.IssuePath, IssueID: candidate.IssueID,
		})
	}
	for _, promotion := range result.Promoted {
		payload.Promoted = append(payload.Promoted, promotionJSON{
			Issue: promotion.IssuePath, IssueID: promotion.IssueID,
			Task: promotion.TaskPath, TaskID: promotion.TaskID,
		})
	}
	for _, skipped := range result.Skipped {
		payload.Skipped = append(payload.Skipped, promotionJSON{
			Issue: skipped.IssuePath, IssueID: skipped.IssueID,
			Skipped: true, Reason: skipped.Reason,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

type promotionJSON struct {
	Issue   string `json:"issue"`
	IssueID string `json:"issue_id"`
	Task    string `json:"task,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason,omitempty"`
}

type promoteJSON struct {
	DryRun     bool            `json:"dry_run,omitempty"`
	Candidates []promotionJSON `json:"candidates,omitempty"`
	Promoted   []promotionJSON `json:"promoted"`
	Skipped    []promotionJSON `json:"skipped"`
}
