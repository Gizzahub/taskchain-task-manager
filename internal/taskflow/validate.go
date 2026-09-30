package taskflow

import (
	"context"
	"fmt"
	"io"
	"os"
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

// validateCard applies the canonical card checks the parity fixtures pin, in
// the order the pinned verdicts print: filename, id, status, type, effort,
// work-task shape, plan counters, plan-zone filing, criteria heading, binding
// shapes, vacuous criteria, and bare-prose path citations.
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
	validateCanonicalStatus(&result, doc, card)
	validateCanonicalType(&result, doc, card)
	validateOptionalEffort(&result, doc)
	validateCanonicalWorkTask(&result, doc, card)
	validatePlanCounters(&result, doc)
	validatePlanZoneFiling(&result, card)
	reportCriteriaHeadingAlias(&result, card)

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
	validateBindingShapes(&result, card.Body, criteria)
	reportVacuousCriteria(ctx, root, card, criteria, &result)
	validatePathCitations(&result, root, card)
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

func validateCanonicalType(result *ValidationResult, doc canonicalDocument, card *Card) {
	cardType, ok := doc.scalar("type")
	if !ok {
		result.Errors = append(result.Errors, Finding{"type", "Missing required frontmatter field: type"})
		return
	}
	// The type is checked against the vocabulary of the kind the id names, not
	// the work-card vocabulary alone: a plan card's types are the plan's own.
	kind := idCardKind(card.ID)
	if !contains(typesForKind(kind), cardType) {
		result.Errors = append(result.Errors,
			Finding{"type", fmt.Sprintf("Invalid type %q for %s", cardType, kind)})
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
	if !hasSummaryHeading(card.Body) {
		result.Errors = append(result.Errors, Finding{"sections", "Missing Summary section"})
	}
	if _, hasHeading := criteriaHeadingLine(card.Body); !hasHeading {
		// A card with no criteria heading at all is missing the section; a
		// card whose heading exists with nothing under it is the emptier
		// "no completion criteria" case below the heading check.
		result.Errors = append(result.Errors, Finding{"sections", "Missing Completion Criteria section"})
	} else if len(card.Criteria()) == 0 {
		result.Errors = append(result.Errors, Finding{"criteria", "card has no completion criteria"})
	}
}

// criteriaHeadingLine returns the first top-level criteria heading outside
// fences, so the alias warning and the missing-section error agree with the
// scanner that grades the section's checkboxes.
func criteriaHeadingLine(body string) (string, bool) {
	var fences FenceScanner
	for _, raw := range strings.Split(body, "\n") {
		if fences.Classify(raw) != OutsideFence {
			continue
		}
		if cardSectionHeading(raw) && IsCriteriaHeading(strings.TrimSpace(raw)) {
			return strings.TrimSpace(raw), true
		}
	}
	return "", false
}

// reportCriteriaHeadingAlias warns when a criteria section arrives under an
// accepted alias spelling: readable forever, but the canonical heading is what
// the board's conventions index.
func reportCriteriaHeadingAlias(result *ValidationResult, card *Card) {
	heading, ok := criteriaHeadingLine(card.Body)
	if !ok {
		return
	}
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(heading), "## "))
	if strings.EqualFold(name, "Completion Criteria") {
		return
	}
	result.Warnings = append(result.Warnings, Finding{"sections",
		fmt.Sprintf("Criteria heading %q is an accepted alias; the canonical heading is \"Completion Criteria\"", name)})
}

// validatePlanCounters applies the plan-counter census: progress counters are
// claims about a roster of children, so a card that declares them with no
// children roster makes a claim nothing on the board can check.
func validatePlanCounters(result *ValidationResult, doc canonicalDocument) {
	declares := false
	for _, key := range []string{"total-tasks", "completed-tasks", "progress"} {
		if _, ok := doc.scalar(key); ok {
			declares = true
			break
		}
	}
	if !declares {
		return
	}
	if node, ok := doc.Fields["children"]; ok && node.Kind != yaml.ScalarNode {
		return // a sequence roster, even an empty one, is a roster
	}
	result.Errors = append(result.Errors, Finding{"plan", "Plan progress counters require a children roster"})
}

// validatePlanZoneFiling warns when a work card is parked under the plan kind
// directory: plan/ holds plan documents, and a task filed there is invisible
// to the zones that move work.
func validatePlanZoneFiling(result *ValidationResult, card *Card) {
	segments := strings.Split(card.TasksRel, "/")
	if len(segments) > 1 && segments[0] == "plan" && idCardKind(card.ID) == "task" {
		result.Warnings = append(result.Warnings,
			Finding{"zone", "task document filed under a plan zone"})
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
		result.Errors = append(result.Errors, Finding{"status", "Invalid status: " + status})
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

// validateBindingShapes applies the pinned binding shape rules to one card's
// criteria population. An unfilled `<...>` placeholder is a warning the
// scaffold itself produces, so it is never also charged as the unbound
// checkbox error it resembles.
func validateBindingShapes(result *ValidationResult, body string, criteria []Criterion) {
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
		text := strings.TrimSpace(criterion.Text)
		if unfilledPlaceholderRe.MatchString(text) {
			result.Warnings = append(result.Warnings, Finding{"criteria",
				fmt.Sprintf("criterion is an unfilled <...> placeholder: %s", text)})
			continue
		}
		if !hasVerifyBindingToken(criterion.Text) {
			result.Errors = append(result.Errors, Finding{"verify",
				fmt.Sprintf("checkbox has no verify binding: %s", text)})
			return
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
		if command, ok := verifyBindingCommand(criterion.Text); ok {
			if shape, severed := severedCommandSubstitution(command); severed {
				result.Errors = append(result.Errors, Finding{"verify",
					fmt.Sprintf("verify binding severs a command substitution with ';', discarding its exit status so the binding cannot go red: %s", shape)})
				return
			}
		}
	}
}

// unfilledPlaceholderRe matches a criterion that is nothing but an angle-bracket
// placeholder, the shape the new-card scaffold writes before its first edit.
var unfilledPlaceholderRe = regexp.MustCompile(`^<[^<>\n]+>$`)

// commandSegment is one ;-separated piece of a verify command, with the class
// of separator that ended it: `;` and `&` discard the exit status of what came
// before them, while `&&` and `||` observe it.
type commandSegment struct {
	Text string
	Sep  string
}

// splitCommandSegments splits a verify command at depth-zero separators,
// tracking quotes and `$(...)` nesting so a substitution's own separators do
// not split what they belong to.
func splitCommandSegments(command string) []commandSegment {
	var segments []commandSegment
	var current strings.Builder
	depth := 0
	var quoted rune
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quoted != 0:
			current.WriteByte(c)
			if rune(c) == quoted {
				quoted = 0
			}
		case c == '"' || c == '\'':
			quoted = rune(c)
			current.WriteByte(c)
		case c == '(':
			depth++
			current.WriteByte(c)
		case c == ')':
			if depth > 0 {
				depth--
			}
			current.WriteByte(c)
		case depth > 0:
			current.WriteByte(c)
		case c == ';' || c == '&' || c == '|':
			run := 1
			for i+1 < len(command) && command[i+1] == c {
				run++
				i++
			}
			segments = append(segments, commandSegment{
				Text: strings.TrimSpace(current.String()),
				Sep:  command[i : i+run],
			})
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	if text := strings.TrimSpace(current.String()); text != "" || len(segments) > 0 {
		segments = append(segments, commandSegment{Text: text})
	}
	return segments
}

// assignmentOfSubstitution reports whether a segment is exactly `name=$(...)`:
// a capture whose exit status lives only in the segment's own semicolon.
func assignmentOfSubstitution(segment string) (string, bool) {
	arrow := strings.Index(segment, "=$(")
	if arrow <= 0 {
		return "", false
	}
	name := segment[:arrow]
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "", false
		}
	}
	rest := segment[arrow+1:]
	if !strings.HasPrefix(rest, "$(") || !strings.HasSuffix(rest, ")") {
		return "", false
	}
	return segment, true
}

// severedCommandSubstitution reports whether a verify command ends a command
// substitution's status on a `;`: `out=$(cmd); more` looks like it captures
// failures, but the `;` discards the substitution's exit status, so the
// binding can never go red. The recognized keeps -- `cmd; rc=$?; test ...` and
// `out=$(cmd) || exit 1` -- observe the status through their separators.
func severedCommandSubstitution(command string) (string, bool) {
	segments := splitCommandSegments(command)
	for _, segment := range segments[:max(len(segments)-1, 0)] {
		if shape, ok := assignmentOfSubstitution(segment.Text); ok && segment.Sep != "" &&
			segment.Sep[0] == ';' {
			return shape, true
		}
	}
	return "", false
}

// zonePathCitationRe matches a bare citation of a numbered card path in prose.
// The file name must start with digits, so an NNN placeholder example is prose
// about the board's shape rather than a claim about one of its files.
var zonePathCitationRe = regexp.MustCompile(`\btasks/[0-9A-Za-z_.-]+/[0-9][0-9A-Za-z_.-]*\.md`)

// validatePathCitations checks bare-prose citations of numbered card paths: a
// citation names a card the board should hold, and one that resolves to
// nothing on disk is a claim about a file that does not exist. Example paths
// belong in backticks; a non-numeric placeholder (NNN) is not a citation.
func validatePathCitations(result *ValidationResult, root string, card *Card) {
	var fences FenceScanner
	var comments htmlCommentScanner
	for _, raw := range strings.Split(card.Body, "\n") {
		if fences.Classify(raw) != OutsideFence {
			continue
		}
		for _, segment := range proseSegments(comments.visible(raw)) {
			for _, citation := range zonePathCitationRe.FindAllString(segment, -1) {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(citation))); err == nil {
					continue
				}
				result.Errors = append(result.Errors, Finding{"citations",
					fmt.Sprintf("Zone-path citation does not resolve on disk: %s -- an example path belongs in backticks, or uses a non-numeric placeholder such as NNN", citation)})
			}
		}
	}
}

// proseSegments splits one line into the pieces outside backtick code spans,
// where a bare citation can be read as prose.
func proseSegments(line string) []string {
	var segments []string
	var current strings.Builder
	for i := 0; i < len(line); {
		if line[i] == '`' {
			if _, end, ok := balancedCodeSpan(line[i:]); ok {
				segments = append(segments, current.String())
				current.Reset()
				i += end
				continue
			}
		}
		current.WriteByte(line[i])
		i++
	}
	return append(segments, current.String())
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
		// An empty board has no summary to add up and no citations to count;
		// the pinned empty-board render ends at the finding itself.
		fmt.Fprintln(w, "No task files found")
		return 0, nil
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
