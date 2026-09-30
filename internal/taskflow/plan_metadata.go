package taskflow

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The plan-metadata corpus pass validates the claims a plan's frontmatter
// makes about a concrete set of child cards: a roster must exist before any
// progress counter is believed, a declared count must equal what the roster
// resolves to, and a child's parent declaration must be reciprocal. It runs
// once per board after the per-card loop, appending errors to the owning
// card's verdict so they render with that card's findings.

// planMetadataCard is the small declared subset needed to check a plan
// rollup. The fields are deliberately optional: this validates claims a
// corpus makes, rather than requiring every card to adopt the plan schema.
type planMetadataCard struct {
	path             string
	id               string
	children         []string
	declaresChildren bool
	parent           string
	declaresParent   bool
	invalidParent    bool
	fields           map[string]yaml.Node
}

type planMetadataCensus struct {
	cards []planMetadataCard
	byRef map[string]int
}

func buildPlanMetadataCensus(cards []*Card) *planMetadataCensus {
	census := &planMetadataCensus{byRef: map[string]int{}}
	for _, card := range cards {
		fields, _, detected, err := parseCanonicalDocument(string(card.Raw))
		if !detected || err != nil {
			continue
		}
		children, declaresChildren := frontmatterList(fields, "children")
		parent, declaresParent, invalidParent := declaredPlanParent(fields)
		census.add(planMetadataCard{
			path:             card.RepoRel(),
			id:               metadataID(fields),
			children:         children,
			declaresChildren: declaresChildren,
			parent:           parent,
			declaresParent:   declaresParent,
			invalidParent:    invalidParent,
			fields:           fields,
		})
	}
	return census
}

// declaredPlanParent keeps the optional parent convention deliberately small:
// a scalar is a reference, while an absent, empty, or null field says this is
// a root card. Collections cannot name one parent and are rejected where the
// child can be fixed.
func declaredPlanParent(fields map[string]yaml.Node) (parent string, declared, invalid bool) {
	node, exists := fields["parent"]
	if !exists || node.Tag == "!!null" {
		return "", false, false
	}
	if node.Kind != yaml.ScalarNode {
		return "", false, true
	}
	parent = strings.TrimSpace(node.Value)
	return parent, parent != "", false
}

func metadataID(fields map[string]yaml.Node) string {
	id, _ := scalarField(fields, "id")
	return strings.TrimSpace(id)
}

func (c *planMetadataCensus) add(card planMetadataCard) {
	idx := len(c.cards)
	c.cards = append(c.cards, card)
	for _, ref := range []string{
		card.id,
		normalizeCardID(card.id),
		strings.TrimSuffix(filepath.Base(card.path), ".md"),
	} {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, exists := c.byRef[ref]; !exists {
			c.byRef[ref] = idx
		}
	}
}

func (c *planMetadataCensus) lookup(ref string) (planMetadataCard, bool) {
	ref = strings.TrimSuffix(strings.TrimSpace(ref), ".md")
	if idx, ok := c.byRef[ref]; ok {
		return c.cards[idx], true
	}
	if normalized := normalizeCardID(ref); normalized != "" {
		if idx, ok := c.byRef[normalized]; ok {
			return c.cards[idx], true
		}
	}
	return planMetadataCard{}, false
}

// frontmatterList reads a sequence frontmatter field as a list of trimmed,
// non-empty strings. A field that is present but not a sequence is reported
// as declared with no values, so the caller can distinguish the shape error.
func frontmatterList(fields map[string]yaml.Node, name string) (values []string, declared bool) {
	node, ok := fields[name]
	if !ok {
		return nil, false
	}
	if node.Kind != yaml.SequenceNode {
		return nil, true
	}
	if err := node.Decode(&values); err != nil {
		return nil, true
	}
	cleaned := values[:0]
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			cleaned = append(cleaned, v)
		}
	}
	return cleaned, true
}

// paddingRe splits an identifier into its prefix and number so TASK-90 and
// TASK-090 are recognised as one card. Zero-padding is a formatting choice;
// only string comparison thinks the two spellings name different work.
var paddingRe = regexp.MustCompile(`^([A-Za-z]+)-0*(\d+)$`)

// normalizeCardID returns the unpadded spelling of an id, or "" when the
// token is not an id at all.
func normalizeCardID(id string) string {
	m := paddingRe.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return ""
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return ""
	}
	return m[1] + "-" + strconv.Itoa(n)
}

// reportInvalidPlanMetadata runs the two corpus loops over the census:
// plan-side roster and rollup checks, then child-side parent reciprocity.
func reportInvalidPlanMetadata(census *planMetadataCensus, results []ValidationResult) {
	live := make(map[string]*ValidationResult, len(results))
	for i := range results {
		live[results[i].Path] = &results[i]
	}
	for _, plan := range census.cards {
		result, livePlan := live[plan.path]
		if !plan.declaresChildren {
			// The counters are derived claims about a concrete set of child
			// cards. Declared without a roster they name no set any tool can
			// resolve. Only a live card can be fixed.
			if livePlan && planDeclaresProgressCounters(plan.fields) {
				result.Errors = append(result.Errors, Finding{"children",
					"Plan progress counters require a children roster"})
			}
			continue
		}
		if !livePlan {
			continue
		}
		childrenNode := plan.fields["children"]
		if childrenNode.Kind != yaml.SequenceNode {
			result.Errors = append(result.Errors, Finding{"children", "Declared children must be a sequence"})
			continue
		}

		completed := 0
		for _, childRef := range plan.children {
			child, found := census.lookup(childRef)
			if !found {
				// A plan can name work not yet made into a card. It is
				// pending, rather than an error or a completed child.
				continue
			}
			if planChildIsComplete(child.path) {
				completed++
			}
			if child.invalidParent {
				continue
			}
			if child.declaresParent && !samePlanReference(child.parent, plan) {
				result.Errors = append(result.Errors, Finding{"children", fmt.Sprintf(
					"Child %s at %s declares parent %q, not this plan",
					strings.TrimSpace(childRef), child.path, child.parent)})
			}
		}

		validateDeclaredPlanNumber(result, plan.fields, "total-tasks", len(plan.children))
		validateDeclaredPlanNumber(result, plan.fields, "completed-tasks", completed)
		validateDeclaredPlanNumber(result, plan.fields, "completed-children", completed)
		progress := 0
		if len(plan.children) > 0 {
			progress = completed * 100 / len(plan.children)
		}
		validateDeclaredPlanNumber(result, plan.fields, "progress", progress)
	}

	// A child-side parent declaration becomes reciprocal only when the named
	// plan has opted into `children:`. That preserves repositories which use
	// a parent label without maintaining a reverse index.
	for _, child := range census.cards {
		if child.invalidParent {
			if result, liveChild := live[child.path]; liveChild {
				result.Errors = append(result.Errors, Finding{"parent",
					"Declared parent must be a scalar reference or null"})
			}
			continue
		}
		if !child.declaresParent {
			continue
		}
		plan, found := census.lookup(child.parent)
		if !found || !plan.declaresChildren || containsPlanChild(census, plan, child.path) {
			continue
		}
		if result, liveChild := live[child.path]; liveChild {
			result.Errors = append(result.Errors, Finding{"parent", fmt.Sprintf(
				"Declared parent %q at %s does not list this child in children",
				child.parent, plan.path)})
		}
	}
}

func validateDeclaredPlanNumber(result *ValidationResult, fields map[string]yaml.Node, field string, want int) {
	got, declared, valid := declaredPlanInteger(fields, field)
	if !declared {
		return
	}
	if !valid {
		result.Errors = append(result.Errors, Finding{field, "Declared " + field + " must be an integer"})
		return
	}
	if got != want {
		result.Errors = append(result.Errors, Finding{field, fmt.Sprintf("Declared %s is %d, want %d from children", field, got, want)})
	}
}

func declaredPlanInteger(fields map[string]yaml.Node, field string) (value int, declared, valid bool) {
	if _, declared = fields[field]; !declared {
		return 0, false, false
	}
	number, err := integerFieldValue(fields, field)
	return number, true, err == nil
}

func integerFieldValue(fields map[string]yaml.Node, field string) (int, error) {
	value, ok := scalarField(fields, field)
	if !ok || value == "" {
		return 0, fmt.Errorf("not an integer")
	}
	return strconv.Atoi(value)
}

// planProgressFields are the rollup counters a plan card may declare: the
// one vocabulary for which fields count as progress claims.
var planProgressFields = []string{"total-tasks", "completed-tasks", "completed-children", "progress"}

func planDeclaresProgressCounters(fields map[string]yaml.Node) bool {
	for _, name := range planProgressFields {
		if _, declared := fields[name]; declared {
			return true
		}
	}
	return false
}

func planChildIsComplete(path string) bool {
	if storageDirFromPath(path) != "" {
		return true
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	if _, status, ok := ZoneSegment(parts); ok {
		return status == StatusDone
	}
	return false
}

func samePlanReference(ref string, plan planMetadataCard) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	for _, candidate := range []string{
		plan.id,
		normalizeCardID(plan.id),
		strings.TrimSuffix(filepath.Base(plan.path), ".md"),
	} {
		if ref == candidate || normalizeCardID(ref) == candidate {
			return true
		}
	}
	return false
}

func containsPlanChild(census *planMetadataCensus, plan planMetadataCard, childPath string) bool {
	for _, ref := range plan.children {
		child, found := census.lookup(ref)
		if found && child.path == childPath {
			return true
		}
	}
	return false
}
