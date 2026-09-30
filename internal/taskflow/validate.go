package taskflow

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReferenceStamp is the validation-tool stamp the CE parity fixtures pin. The
// fixtures were captured against CE bb970b24; the byte contract includes this
// line, so the port prints the pinned reference stamp it is measured against.
const ReferenceStamp = "Validation tool: CE v0.8.4-400-gbb970b24 (bb970b24adfc29f75e7555cb3cc45716d6973f3b)"

// ReferenceStampVersion and ReferenceStampRevision are the two halves of that
// stamp, carried separately because the gate's machine verdict reports them
// as distinct fields a consumer can quote.
const (
	ReferenceStampVersion  = "v0.8.4-400-gbb970b24"
	ReferenceStampRevision = "bb970b24adfc29f75e7555cb3cc45716d6973f3b"
)

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

// Valid reports whether the card passes: errors fail it, warnings do not. A
// card with only warnings is "valid with warnings" — advisory, still valid.
func (r *ValidationResult) Valid() bool { return len(r.Errors) == 0 }

func (r *ValidationResult) addError(field, message string) {
	r.Errors = append(r.Errors, Finding{Field: field, Message: message})
}

func (r *ValidationResult) addWarning(field, message string) {
	r.Warnings = append(r.Warnings, Finding{Field: field, Message: message})
}

// Citations counts the path bindings validate examined across the board.
type Citations struct {
	Examined int
	Skipped  int
}

var cardFilenameShapeRe = regexp.MustCompile(`^([0-9]{2,3}|P[0-9])-[a-z0-9]+(-[a-z0-9]+)*\.md$`)

// --- per-card validation --------------------------------------------------------
// validateCard applies the canonical card checks in the pinned reference's
// order. The order is load-bearing: findings render errors-then-warnings per
// card, and the fixture contract pins the sequence within each list.

func validateCard(ctx context.Context, root, tasksDir string, card *Card, index *corpusIndex, citations *Citations) ValidationResult {
	result := ValidationResult{Path: pathJoin(tasksDir, card.TasksRel)}
	fields, body, detected, err := parseCanonicalDocument(string(card.Raw))
	if !detected {
		return result
	}
	if err != nil {
		result.Errors = append(result.Errors, Finding{"frontmatter", "Invalid YAML frontmatter: " + err.Error()})
		return result
	}
	// A file with neither an id nor a type key is not canonical-shaped; the
	// schema checks do not apply to it.
	if _, hasID := fields["id"]; !hasID {
		if _, hasType := fields["type"]; !hasType {
			return result
		}
	}

	kind, ok := resolveCanonicalKind(card.RepoRel(), fields, &result)
	if !ok {
		// The file carries canonical task frontmatter, has no usable ID to
		// derive its kind from, and lives in a directory this schema does not
		// own. Skip it with a warning rather than a hard error.
		result.Warnings = append(result.Warnings, Finding{"location", "Skipped: no CE schema for this task directory"})
		return result
	}
	validateCanonicalFilename(&result, card)
	if kind != canonicalPlan {
		validateCanonicalStatus(card.RepoRel(), fields, &result)
	}
	validateZonePathCitations(&result, card, index)
	warnDoingZoneExit(&result, card)

	// The default dialect requires an id on work cards, and every other kind
	// keeps it: plan, issue, and backlog documents are identified by their
	// prefix, so id, title, and type are required throughout.
	requireScalarFields(&result, fields, "id", "title", "type")
	validateCanonicalID(&result, fields)
	validateCanonicalType(&result, fields, kind)
	validateOptionalEffort(&result, fields)

	switch kind {
	case canonicalTask:
		validateCanonicalWorkTask(&result, fields, body)
	case canonicalPlan:
		validateCanonicalPlan(&result, fields, body)
	case canonicalIssue, canonicalBacklog:
		// The issue and backlog schemas are not exercised by any pinned
		// fixture and are deliberate divergences of this port; a card whose
		// id or zone names those kinds still gets every shared check.
	}

	warnPlaceholderFields(&result, fields)
	validateDependencyFieldShapes(&result, fields)

	reportVacuousCriteria(ctx, root, card, &result)

	examined, skipped := countPathBindings(criterionLines(body))
	citations.Examined += examined
	citations.Skipped += skipped

	validateBindingShape(&result, body)
	return result
}

func validateCanonicalFilename(result *ValidationResult, card *Card) {
	if cardFilenameShapeRe.MatchString(filepath.Base(card.TasksRel)) {
		return
	}
	result.Warnings = append(result.Warnings,
		Finding{"filename", "Non-standard filename (should be ##-kebab-case.md or P#-kebab-case.md)"})
}

// warnDoingZoneExit warns when a doing card's every criterion is checked but
// the card never left the zone: the work looks finished, the board disagrees.
func warnDoingZoneExit(result *ValidationResult, card *Card) {
	zone, ok := statusZone(card.RepoRel())
	if !ok || zone != StatusInProgress.Dir() {
		return
	}
	lines := criterionLines(card.Body)
	if len(lines) == 0 {
		return
	}
	for _, line := range lines {
		if !line.Checked {
			return
		}
	}
	result.Warnings = append(result.Warnings, Finding{"doing_zone_exit",
		"every criterion is checked while the card is still in doing"})
}

func requireScalarFields(result *ValidationResult, fields map[string]yaml.Node, names ...string) {
	for _, name := range names {
		value, ok := scalarField(fields, name)
		if !ok || value == "" {
			result.Errors = append(result.Errors, Finding{name, "Missing required frontmatter field: " + name})
		}
	}
}

// validateCanonicalID checks the ID's shape only. The prefix cannot be wrong
// for the kind — resolveCanonicalKind derives the kind from it — so the only
// failure left is an ID that does not parse as PREFIX-NNN.
func validateCanonicalID(result *ValidationResult, fields map[string]yaml.Node) {
	id, ok := scalarField(fields, "id")
	if !ok || id == "" {
		return
	}
	if _, _, ok := parseCanonicalTaskID(id); !ok {
		result.Errors = append(result.Errors, Finding{"id", "Invalid canonical task ID: " + id})
	}
}

// validateCanonicalType checks `type:` against the set for the document kind.
// Only the work-card set comes from the product's own vocabulary; plan, issue,
// and backlog documents carry the reference's own taxonomy.
func validateCanonicalType(result *ValidationResult, fields map[string]yaml.Node, kind canonicalTaskKind) {
	value, ok := scalarField(fields, "type")
	if !ok || value == "" {
		return
	}
	allowed := map[canonicalTaskKind][]string{
		canonicalTask:    TaskTypes,
		canonicalPlan:    {"plan", "roadmap", "phase"},
		canonicalIssue:   {"bug", "blocker", "tech-debt"},
		canonicalBacklog: {"idea", "feature", "improvement", "tech-debt"},
	}
	if !contains(allowed[kind], value) {
		result.Errors = append(result.Errors, Finding{"type", fmt.Sprintf("Invalid type %q for %s", value, kind)})
	}
}

func validateOptionalEffort(result *ValidationResult, fields map[string]yaml.Node) {
	value, ok := scalarField(fields, "effort")
	if !ok || value == "" {
		return
	}
	if !contains([]string{"XS", "S", "M", "L", "XL", "unknown"}, value) {
		result.Errors = append(result.Errors, Finding{"effort", "Invalid effort: " + value})
	}
}

// validateCanonicalStatus keeps the frontmatter state claim aligned with the
// directory state. The reader deliberately lets the directory win so list and
// preflight stay safe, but validate must report a malformed claim instead of
// silently discarding it.
func validateCanonicalStatus(path string, fields map[string]yaml.Node, result *ValidationResult) {
	value, present := scalarField(fields, "status")
	if !present {
		return
	}
	if value == "" {
		result.Errors = append(result.Errors, Finding{"status", "status must not be empty"})
		return
	}
	zone, ok := statusZone(path)
	if !ok {
		if _, recognised := StatusFromWord(value); !recognised && value != "backlog" && value != supersededTerminal {
			result.Errors = append(result.Errors, Finding{"status", "Invalid status: " + value})
		}
		return
	}
	if zone == "backlog" {
		if strings.ToLower(strings.TrimSpace(value)) != "backlog" {
			result.Errors = append(result.Errors, Finding{"status",
				fmt.Sprintf("status %q does not match zone %q (expected backlog)", value, zone)})
		}
		return
	}
	if zone == StorageWriteDir || zone == LegacyStorageDir {
		if value != StatusDone.Dir() && value != supersededTerminal {
			result.Errors = append(result.Errors, Finding{"status",
				fmt.Sprintf("status %q is not permitted in archive zone (expected done or %s)", value, supersededTerminal)})
		}
		return
	}
	if zone == "issue" {
		if !statusMatches(value, StatusPending) {
			result.Errors = append(result.Errors, Finding{"status",
				fmt.Sprintf("status %q does not match zone %q (expected todo)", value, zone)})
		}
		return
	}
	expected, expectedOK := StatusFromDir(zone)
	if !expectedOK || !statusMatches(value, expected) {
		result.Errors = append(result.Errors, Finding{"status",
			fmt.Sprintf("status %q does not match zone %q (expected %s)", value, zone, expected.Dir())})
	}
}

// validateCanonicalWorkTask is the work-card shape: a priority from the
// dialect's scale, a Summary heading, and a completion-criteria section.
func validateCanonicalWorkTask(result *ValidationResult, fields map[string]yaml.Node, body string) {
	requireScalarFields(result, fields, "priority")
	if priority, ok := scalarField(fields, "priority"); ok && priority != "" && !contains(PriorityValues, priority) {
		result.Errors = append(result.Errors, Finding{"priority", "Invalid priority: " + priority})
	}
	requireHeading(result, body, "Summary")
	requireCriteria(result, body, "Completion Criteria")
}

// validateCanonicalPlan is the plan-document shape: the rollup counters it
// reports on, a children roster, and the two structural headings.
func validateCanonicalPlan(result *ValidationResult, fields map[string]yaml.Node, body string) {
	requireScalarFields(result, fields, "scope", "progress", "total-tasks", "completed-tasks")
	for _, name := range []string{"children", "target-date"} {
		if _, present := fields[name]; !present {
			result.Errors = append(result.Errors, Finding{name, "Missing required frontmatter field: " + name})
		}
	}
	validatePlanCounts(result, fields)
	requireHeading(result, body, "Goal")
	requireHeading(result, body, "Children")
}

func validatePlanCounts(result *ValidationResult, fields map[string]yaml.Node) {
	children, ok := fields["children"]
	if ok && children.Kind != yaml.SequenceNode {
		result.Errors = append(result.Errors, Finding{"children", "Frontmatter field children must be a sequence"})
		return
	}
	atoi := func(name string) (int, bool) {
		value, ok := scalarField(fields, name)
		if !ok || value == "" {
			return 0, false
		}
		n, err := strconv.Atoi(value)
		return n, err == nil
	}
	total, totalOK := atoi("total-tasks")
	completed, completedOK := atoi("completed-tasks")
	progress, progressOK := atoi("progress")
	if totalOK && ok && total != len(children.Content) {
		result.Errors = append(result.Errors, Finding{"total-tasks", "total-tasks must equal the number of children"})
	}
	if totalOK && completedOK && (completed < 0 || completed > total) {
		result.Errors = append(result.Errors, Finding{"completed-tasks", "completed-tasks must be between 0 and total-tasks"})
	}
	if progressOK && (progress < 0 || progress > 100) {
		result.Errors = append(result.Errors, Finding{"progress", "progress must be between 0 and 100"})
	}
}

// requireHeading demands a literal `## <heading>` line in the body. The
// pattern reads the rendered card, so a heading inside a fenced example still
// satisfies it — the pinned check is textual, not structural.
func requireHeading(result *ValidationResult, body, heading string) {
	pattern := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(heading) + `\s*$`)
	if !pattern.MatchString(body) {
		result.Errors = append(result.Errors, Finding{
			strings.ToLower(strings.ReplaceAll(heading, " ", "_")), "Missing " + heading + " section"})
	}
}

// requireCriteria enforces the criteria-section half of the population rule
// through the same selection the scanner and the card reader use, so validate
// can no longer refuse a section preflight reads — or accept one preflight
// cannot see. An accepted alias heading satisfies the presence requirement
// and is reported with the canonical name: the corpus carries several
// spellings, and a hard refusal would break boards the reader already serves.
func requireCriteria(result *ValidationResult, body, heading string) {
	section, _, found, ok := findCriteriaSection(body)
	if !ok {
		result.Errors = append(result.Errors, Finding{"completion_criteria", "Missing " + heading + " section"})
		return
	}
	if found != heading {
		result.Warnings = append(result.Warnings, Finding{"criteria_heading", fmt.Sprintf(
			"Criteria heading %q is an accepted alias; the canonical heading is %q", found, heading)})
	}
	// Commented-out template examples do not count: the scanner reads what
	// the card renders, and a section whose only checkboxes are inside an
	// HTML comment defines nothing.
	if len(scanCriteriaFrom(section, 1).Lines) == 0 {
		result.Errors = append(result.Errors, Finding{"completion_criteria", "No criteria defined under " + found})
	}
	warnMalformedVerifyBindings(result, section)
}

// warnMalformedVerifyBindings checks the verify markers of one criteria
// section: an orphan marker (no checkbox owns it) and a command truncated by
// an unclosed fence are errors; a value that is neither a backtick command
// nor `human — …` is a warning. First finding wins — each is a reading the
// later shape checks cannot improve on.
func warnMalformedVerifyBindings(result *ValidationResult, section string) {
	scan := scanCriteriaFrom(section, 1)
	if scan.OrphanVerifyMarker {
		result.Errors = append(result.Errors, Finding{"verify",
			"a | verify: binding must be on the same checkbox line as its completion criterion"})
		return
	}
	for _, criterion := range scan.Lines {
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

// warnPlaceholderFields warns on scalar frontmatter values still carrying the
// scaffold's `<...>` placeholder shape.
func warnPlaceholderFields(result *ValidationResult, fields map[string]yaml.Node) {
	for name, node := range fields {
		if node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
			continue
		}
		value := strings.TrimSpace(node.Value)
		if placeholderValueRe.MatchString(value) {
			result.Warnings = append(result.Warnings, Finding{name,
				"placeholder value " + value + " is still unfilled"})
		}
	}
}

// validateDependencyFieldShapes rejects frontmatter dependency fields whose
// YAML node kind is not a sequence. A scalar `blocks: TASK-999` is an
// unreadable shape that would otherwise quietly escape edge census.
func validateDependencyFieldShapes(result *ValidationResult, fields map[string]yaml.Node) {
	for _, name := range []string{"blocks", "depends-on"} {
		node, ok := fields[name]
		if !ok || node.Tag == "!!null" {
			continue
		}
		if node.Kind != yaml.SequenceNode {
			result.Errors = append(result.Errors, Finding{name, fmt.Sprintf(
				"Frontmatter field %s must be a sequence, got %s", name, yamlKindName(node.Kind))})
		}
	}
}

func yamlKindName(kind yaml.Kind) string {
	switch kind {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return "unknown"
	}
}

// reportVacuousCriteria probes unchecked command bindings of todo cards and
// reports the ones the tree already satisfies.
func reportVacuousCriteria(ctx context.Context, root string, card *Card, result *ValidationResult) {
	vacuous, err := vacuousCriteria(ctx, root, card.Zone, card.Criteria())
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

// ValidateAll validates every live card and returns the per-card verdicts in
// walk order.
func ValidateAll(ctx context.Context, root string) ([]ValidationResult, Citations, error) {
	return ValidateAllIn(ctx, root, tasksDirName())
}

// ValidateAllIn validates the board rooted at tasksDir — the directory
// TASKS_DIR redirects the walk at, e.g. a decisions corpus — and returns the
// per-document verdicts in walk order. Decision documents under the scan root
// are validated by the decision schema, not the card schema, and the
// decisions index corpus rule appends its synthetic README verdict when the
// catalog drifts.
func ValidateAllIn(ctx context.Context, root, tasksDir string) ([]ValidationResult, Citations, error) {
	cards, err := FindCardsIn(root, tasksDir, true)
	if err != nil {
		return nil, Citations{}, err
	}
	index := buildCorpusIndex(root)
	citations := Citations{}
	results := make([]ValidationResult, 0, len(cards))
	for _, card := range cards {
		if isDecisionDoc(card.TasksRel) && !isNonCardFile(filepath.Base(card.TasksRel)) {
			results = append(results, validateDecisionDocument(ctx, root, tasksDir, card))
			continue
		}
		results = append(results, validateCard(ctx, root, tasksDir, card, index, &citations))
	}
	// Corpus-level plan-metadata findings fold into the cards they belong to.
	reportInvalidPlanMetadata(buildPlanMetadataCensus(cards), results)
	// The decisions index is a corpus rule whose finding belongs to a file the
	// walk never yields: README.md is not a card. It gets a result of its own
	// so the drift is counted and printed like any other failure.
	files := make([]string, 0, len(cards))
	for _, card := range cards {
		files = append(files, pathJoin(tasksDir, card.TasksRel))
	}
	if index := reportDecisionIndexDrift(root, files, tasksDir); index != nil && !index.Valid() {
		results = append(results, *index)
	}
	return results, citations, nil
}

// validateDecisionDocument validates one ADR/decision document by the
// decision schema. It never touches the citations census: decision documents
// carry no verify bindings.
func validateDecisionDocument(ctx context.Context, root, tasksDir string, card *Card) ValidationResult {
	result := ValidationResult{Path: pathJoin(tasksDir, card.TasksRel)}
	content := string(card.Raw)
	validateDecisionDoc(content, &result)
	validateDecisionLinks(ctx, root, result.Path, content, &result)
	return result
}

// RenderValidateAll writes the whole-board validate output. It returns the
// number of invalid cards for the caller's exit decision.
func RenderValidateAll(ctx context.Context, w io.Writer, root string) (int, error) {
	return RenderValidateAllIn(ctx, w, root, tasksDirName())
}

// RenderValidateAllIn writes the whole-board validate output for the board
// rooted at tasksDir.
func RenderValidateAllIn(ctx context.Context, w io.Writer, root, tasksDir string) (int, error) {
	results, citations, err := ValidateAllIn(ctx, root, tasksDir)
	if err != nil {
		return 0, err
	}
	fmt.Fprintln(w, ReferenceStamp)
	fmt.Fprintln(w, "📋 Validating all tasks...")
	fmt.Fprintln(w)
	if len(results) == 0 {
		// An empty board ends the report after the bare notice: there are no
		// per-card verdicts to sum, so the ---, the summary, and the citation
		// counter have nothing to count and are omitted.
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
