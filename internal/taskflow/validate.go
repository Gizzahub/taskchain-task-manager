package taskflow

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReferenceStamp is the validation-tool stamp the CE parity fixtures pin. The
// fixtures were captured against CE bb970b24; the byte contract includes this
// line, so the port prints the pinned reference stamp it is measured against.
const ReferenceStamp = "Validation tool: CE v0.8.4-400-gbb970b24 (bb970b24adfc29f75e7555cb3cc45716d6973f3b)"

// Finding is one validation error or warning. Field names the schema key the
// finding is about; rendering shows only the message.
type Finding struct {
	Field   string
	Message string
}

// ValidationResult is one card's verdict.
type ValidationResult struct {
	Path     string
	Errors   []Finding
	Warnings []Finding
}

// Valid reports a clean verdict: no errors and no warnings. A card with only
// warnings is "valid with warnings", which is still not clean.
func (r *ValidationResult) Valid() bool { return len(r.Errors) == 0 && len(r.Warnings) == 0 }

// Citations counts the path bindings validate examined across the board.
type Citations struct {
	Examined int
	Skipped  int
}

var cardIDShapeRe = regexp.MustCompile(`^(TASK|PLAN|ISSUE|BACKLOG)-[0-9]+$`)

var cardFilenameShapeRe = regexp.MustCompile(`^([0-9]{2,3}|P[0-9])-[a-z0-9]+(-[a-z0-9]+)*\.md$`)

// canonicalDocument is a parsed frontmatter block kept in yaml node form, so
// an unfilled or malformed value can still be reported.
type canonicalDocument struct {
	Fields map[string]yaml.Node
	HasFM  bool
}

func parseCanonicalDocument(raw []byte) canonicalDocument {
	doc := canonicalDocument{Fields: map[string]yaml.Node{}}
	fm, _ := splitFrontmatter(raw)
	if fm == nil {
		return doc
	}
	doc.HasFM = true
	var parsed map[string]yaml.Node
	if err := yaml.Unmarshal(fm, &parsed); err != nil {
		return doc
	}
	doc.Fields = parsed
	return doc
}

func (d canonicalDocument) scalar(name string) (string, bool) {
	node, ok := d.Fields[name]
	if !ok || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return "", false
	}
	return strings.TrimSpace(node.Value), true
}

// validateCard applies the canonical card checks the card-core contract pins.
// The remaining validation-gate checks (alias warnings, binding-shape errors,
// placeholder findings, gate receipts) are later bundles' scope; the skeleton
// below is where they slot in.
func validateCard(ctx context.Context, root string, card *Card, citations *Citations) ValidationResult {
	result := ValidationResult{Path: card.RepoRel()}
	doc := parseCanonicalDocument(card.Raw)

	// Detection: a card is canonical-shaped when a frontmatter fence exists
	// and carries an id or type. Anything else is not schema-checked here.
	if !doc.HasFM {
		result.Errors = append(result.Errors, Finding{"frontmatter", "task file has no frontmatter"})
		return result
	}

	validateCanonicalFilename(&result, card)
	validateCanonicalID(&result, doc)
	validateCanonicalType(&result, doc)
	validateOptionalEffort(&result, doc)
	validateCanonicalWorkTask(&result, doc, card)
	validateCanonicalStatus(&result, doc, card)

	criteria := card.Criteria()
	for _, criterion := range criteria {
		// Citations count every command binding the board shows, valid or not:
		// the summary line reports how much of the board's verification was
		// even examinable without a shell.
		if criterion.Command != "" {
			if classifyProbe(criterion.Command) == nil {
				citations.Examined++
			} else {
				citations.Skipped++
			}
		}
	}
	orphanAndMalformedBindings(&result, card.Body, criteria)
	reportVacuousCriteria(ctx, root, card, criteria, &result)

	// TODO(validation-gate bundle): corpus rules (duplicate ids, filename-id
	// agreement, one-sided dependency edges, plan metadata, decision index),
	// placeholder-value warnings, absence-asserted verify-path warnings, gate
	// receipts, and the single-card `validate <path>` mode.
	return result
}

func validateCanonicalFilename(result *ValidationResult, card *Card) {
	base := filepath.Base(card.TasksRel)
	if !cardFilenameShapeRe.MatchString(base) {
		result.Warnings = append(result.Warnings,
			Finding{"filename", "Non-standard filename (should be ##-kebab-case.md or P#-kebab-case.md)"})
	}
}

func validateCanonicalID(result *ValidationResult, doc canonicalDocument) {
	id, ok := doc.scalar("id")
	if !ok {
		result.Errors = append(result.Errors, Finding{"id", "Missing required frontmatter field: id"})
		return
	}
	if !cardIDShapeRe.MatchString(id) {
		result.Errors = append(result.Errors, Finding{"id", "Invalid id: " + id})
	}
}

func validateCanonicalType(result *ValidationResult, doc canonicalDocument) {
	cardType, ok := doc.scalar("type")
	if !ok {
		result.Errors = append(result.Errors, Finding{"type", "Missing required frontmatter field: type"})
		return
	}
	if !contains(TaskTypes, cardType) {
		result.Errors = append(result.Errors, Finding{"type", "Invalid type: " + cardType})
	}
}

func validateOptionalEffort(result *ValidationResult, doc canonicalDocument) {
	effort, ok := doc.scalar("effort")
	if !ok {
		return
	}
	if !contains([]string{"XS", "S", "M", "L", "XL"}, effort) {
		result.Errors = append(result.Errors, Finding{"effort", "Invalid effort: " + effort})
	}
}

func validateCanonicalWorkTask(result *ValidationResult, doc canonicalDocument, card *Card) {
	priority, ok := doc.scalar("priority")
	if ok && !contains(PriorityValues, priority) {
		result.Errors = append(result.Errors, Finding{"priority", "Invalid priority: " + priority})
	}
	if _, ok := doc.scalar("title"); !ok {
		result.Errors = append(result.Errors, Finding{"title", "Missing required frontmatter field: title"})
	}
	if !documentHasHeading(card.Body) || !hasSummaryHeading(card.Body) {
		result.Errors = append(result.Errors, Finding{"sections", "Missing required section heading: Summary"})
	}
	if len(card.Criteria()) == 0 {
		result.Errors = append(result.Errors, Finding{"criteria", "card has no completion criteria"})
	}
}

func hasSummaryHeading(body string) bool {
	var fences FenceScanner
	for _, raw := range strings.Split(body, "\n") {
		if fences.Classify(raw) != OutsideFence {
			continue
		}
		if cardSectionHeading(raw) && strings.EqualFold(strings.TrimSpace(raw), "## Summary") {
			return true
		}
	}
	return false
}

func validateCanonicalStatus(result *ValidationResult, doc canonicalDocument, card *Card) {
	status, ok := doc.scalar("status")
	if !ok {
		// A missing status is an error only where a status is required; the
		// default dialect's zone directory carries the state instead.
		return
	}
	if status == "" {
		result.Errors = append(result.Errors, Finding{"status", "status is present but empty"})
		return
	}
	if card.Zone != "" {
		return // the zone segment is authoritative and already matched
	}
	if _, known := StatusFromWord(status); !known {
		result.Errors = append(result.Errors, Finding{"status", "Unknown status: " + status})
	}
}

// reportVacuousCriteria probes unchecked command bindings of todo cards and
// reports the ones the tree already satisfies.
func reportVacuousCriteria(ctx context.Context, root string, card *Card, criteria []Criterion, result *ValidationResult) {
	vacuous, err := vacuousCriteria(ctx, root, card.Zone, criteria)
	if err != nil {
		return
	}
	for _, criterion := range vacuous {
		result.Errors = append(result.Errors, Finding{
			"verify",
			fmt.Sprintf("Criterion already passes on the current tree, so completing this card would prove nothing: %s", criterion.Text),
		})
	}
}

// orphanAndMalformedBindings applies the pinned binding shape rules to one
// card's criteria population.
func orphanAndMalformedBindings(result *ValidationResult, body string, criteria []Criterion) {
	var scan criteriaScan
	var fences FenceScanner
	var comments htmlCommentScanner
	for _, raw := range strings.Split(body, "\n") {
		if comments.inComment {
			visible := comments.visible(raw)
			if fences.Classify(visible) == InsideFence {
				continue
			}
			scanVisibleCriteriaLine(&scan, visible, 0)
			continue
		}
		if fences.Classify(raw) != OutsideFence {
			continue
		}
		scanVisibleCriteriaLine(&scan, comments.visible(raw), 0)
	}
	if scan.OrphanVerifyMarker {
		result.Errors = append(result.Errors, Finding{"verify",
			"a | verify: binding must be on the same checkbox line as its completion criterion"})
		return
	}
	for _, criterion := range criteria {
		if !hasVerifyBindingToken(criterion.Text) {
			continue
		}
		if truncatedSingleFence(criterion.Text) {
			result.Errors = append(result.Errors, Finding{"verify", truncationHint})
			return
		}
		if !verifyBindingValid(criterion.Text) {
			result.Warnings = append(result.Warnings, Finding{"verify",
				"a | verify: value is neither a backtick command nor `human — …`"})
			return
		}
	}
}

const truncationHint = "a | verify: command looks truncated by an unclosed code fence"

// ValidateAll validates every live card and returns the per-card verdicts in
// walk order.
func ValidateAll(ctx context.Context, root string) ([]ValidationResult, Citations, error) {
	cards, err := FindCards(root, true)
	if err != nil {
		return nil, Citations{}, err
	}
	citations := Citations{}
	results := make([]ValidationResult, 0, len(cards))
	for _, card := range cards {
		results = append(results, validateCard(ctx, root, card, &citations))
	}
	return results, citations, nil
}

// RenderValidateAll writes the whole-board validate output. It returns the
// number of invalid cards for the caller's exit decision.
func RenderValidateAll(ctx context.Context, w io.Writer, root string) (int, error) {
	results, citations, err := ValidateAll(ctx, root)
	if err != nil {
		return 0, err
	}
	fmt.Fprintln(w, ReferenceStamp)
	fmt.Fprintln(w, "📋 Validating all tasks...")
	fmt.Fprintln(w)
	if len(results) == 0 {
		fmt.Fprintln(w, "No task files found")
	}
	valid := 0
	invalid := 0
	for _, result := range results {
		fmt.Fprintf(w, "Validating: %s\n", result.Path)
		for _, e := range result.Errors {
			fmt.Fprintf(w, "  ❌ %s\n", e.Message)
		}
		for _, warn := range result.Warnings {
			fmt.Fprintf(w, "  ⚠️  %s\n", warn.Message)
		}
		switch {
		case len(result.Errors) == 0 && len(result.Warnings) == 0:
			valid++
			fmt.Fprintln(w, "  ✅ Valid (no errors or warnings)")
		case len(result.Errors) == 0:
			valid++
			fmt.Fprintf(w, "  ⚠️  Valid with %d warning(s)\n", len(result.Warnings))
		default:
			invalid++
			fmt.Fprintf(w, "  ❌ Invalid: %d error(s), %d warning(s)\n", len(result.Errors), len(result.Warnings))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "---")
	fmt.Fprintf(w, "Summary: %d valid, %d invalid (total: %d)\n", valid, invalid, len(results))
	fmt.Fprintf(w, "Path citations: %d binding(s) examined, %d skipped as unreadable without a shell\n",
		citations.Examined, citations.Skipped)
	return invalid, nil
}
