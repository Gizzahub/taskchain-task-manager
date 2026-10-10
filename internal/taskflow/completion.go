package taskflow

import (
	"path/filepath"
	"regexp"
	"strings"
)

// completionIndex answers two questions about depends-on: whether the
// predecessor is finished, and whether any card in the corpus carries the id.
type completionIndex struct {
	finished cardIdentifiers
	known    cardIdentifiers
}

type cardIdentifiers struct {
	ids       []string
	fileIDs   map[string]struct{}
	foldedIDs []string
}

type dependencyGaps struct {
	Unmet        []string
	Dangling     []string
	CaseMismatch []string
}

func (g dependencyGaps) empty() bool {
	return len(g.Unmet) == 0 && len(g.Dangling) == 0 && len(g.CaseMismatch) == 0
}

// safeFileName is the file: identity grammar: one basename, ASCII, no spaces.
var safeFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.md$`)

func (c *cardIdentifiers) add(id string) {
	id = strings.TrimSuffix(strings.TrimSpace(id), ".md")
	if id == "" {
		return
	}
	c.ids = append(c.ids, id)
	c.foldedIDs = append(c.foldedIDs, strings.ToLower(id))
	if norm := normalizeCardID(id); norm != "" && norm != id {
		c.ids = append(c.ids, norm)
		c.foldedIDs = append(c.foldedIDs, strings.ToLower(norm))
	}
}

func (c *cardIdentifiers) addFileIdentifier(id string) {
	if !safeFileName.MatchString(id) {
		return
	}
	if c.fileIDs == nil {
		c.fileIDs = make(map[string]struct{})
	}
	c.fileIDs[id] = struct{}{}
}

func (c *cardIdentifiers) matches(dep string) bool {
	trimmed := strings.TrimSpace(dep)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "file:") {
		if dep != trimmed {
			return false
		}
		name := strings.TrimPrefix(dep, "file:")
		if !safeFileName.MatchString(name) {
			return false
		}
		_, ok := c.fileIDs[name]
		return ok
	}
	dep = strings.TrimSuffix(trimmed, ".md")
	if depSatisfied(dep, c.ids) {
		return true
	}
	if norm := normalizeCardID(dep); norm != "" && norm != dep {
		return depSatisfied(norm, c.ids)
	}
	return false
}

func (c *cardIdentifiers) matchesFold(dep string) bool {
	trimmed := strings.TrimSpace(dep)
	if trimmed == "" || strings.HasPrefix(trimmed, "file:") {
		return false
	}
	folded := strings.ToLower(strings.TrimSuffix(trimmed, ".md"))
	if depSatisfied(folded, c.foldedIDs) {
		return true
	}
	if norm := normalizeCardID(folded); norm != "" && norm != folded {
		return depSatisfied(norm, c.foldedIDs)
	}
	return false
}

func depSatisfied(dep string, ids []string) bool {
	dep = strings.TrimSuffix(strings.TrimSpace(dep), ".md")
	if dep == "" {
		return true
	}
	for _, id := range ids {
		if id == dep || strings.HasPrefix(id, dep+"-") {
			return true
		}
	}
	return false
}

func (c *completionIndex) satisfied(dep string) bool { return c.finished.matches(dep) }

func (c *completionIndex) gaps(deps []string) dependencyGaps {
	var g dependencyGaps
	for _, dep := range deps {
		if c.satisfied(dep) {
			continue
		}
		trimmed := strings.TrimSpace(dep)
		if c.known.matches(dep) {
			g.Unmet = append(g.Unmet, trimmed)
			continue
		}
		if c.known.matchesFold(dep) {
			g.CaseMismatch = append(g.CaseMismatch, trimmed)
			continue
		}
		g.Dangling = append(g.Dangling, trimmed)
	}
	return g
}

// unfinished reports a promoted-to id whose card is not finished. An empty
// token is not a live link.
func (c *completionIndex) unfinished(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	return !c.satisfied(id)
}

func cardFinished(card *Card) bool {
	if card == nil {
		return false
	}
	if storageDir(card.TasksRel) != "" || card.Zone == "done" || card.Status == StatusDone {
		return true
	}
	return false
}

func buildCompletionIndex(cards []*Card) *completionIndex {
	index := &completionIndex{}
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
	for _, card := range cards {
		if card == nil {
			continue
		}
		targets := []*cardIdentifiers{&index.known}
		if cardFinished(card) {
			targets = append(targets, &index.finished)
		}
		add := func(id string) {
			for _, target := range targets {
				target.add(id)
			}
		}
		add(strings.TrimSuffix(filepath.Base(card.RepoRel()), ".md"))
		add(card.ID)
		name := filepath.Base(card.RepoRel())
		if card.ID == "" && claims[name] == 1 && closedFrontmatter(card.Raw) {
			for _, target := range targets {
				target.addFileIdentifier(name)
			}
		}
	}
	return index
}

func closedFrontmatter(raw []byte) bool {
	fm, _ := splitFrontmatter(raw)
	return fm != nil
}
