package taskflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Decision/ADR document validation, ported from the pinned CE reference
// (bb970b24, internal/usecase/task/canonical_validator_decision.go and
// decision_index*.go). These documents follow an ADR markdown layout
// (# ADR-NNN:, "- Status:" list), not task YAML frontmatter, so they get a
// dedicated validator instead of the canonical card path.

// isDecisionDoc reports whether path is an ADR/decision document by its
// directory names: any path segment named decision(s)/adr(s) files it under
// the decision domain.
func isDecisionDoc(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		if part == "decision" || part == "decisions" || part == "adr" || part == "adrs" {
			return true
		}
	}
	return false
}

// isNonCardFile reports whether name is a catalog/index file rather than a
// card or decision document.
func isNonCardFile(name string) bool {
	switch strings.ToLower(name) {
	case "readme.md", "index.md", "template.md":
		return true
	default:
		return false
	}
}

// canonicalFrozenZone reports whether path sits in long-term storage.
// Archived documents were written against whatever schema was current when
// they were filed; holding them to today's produces churn no one reads.
func canonicalFrozenZone(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		if IsStorageDir(part) {
			return true
		}
	}
	return false
}

// decisionStatusPattern accepts the common Status spellings authors actually
// write: list (`- Status:`), bare/frontmatter (`status:`), and bold markdown
// (`**Status**:`).
var decisionStatusPattern = regexp.MustCompile(`(?mi)^\s*-?\s*\*{0,2}Status\*{0,2}:\s*([A-Za-z]+)`)

// validateDecisionDoc enforces the decision schema's Validation Rules: a
// Status from the ADR enum and the four required sections (Context, Decision,
// Rationale, Consequences). File naming, Date, and Follow-ups are advisory
// and left to the authoring skill.
func validateDecisionDoc(content string, result *ValidationResult) {
	if m := decisionStatusPattern.FindStringSubmatch(content); m == nil {
		result.addError("status", "Missing Status (Proposed|Accepted|Rejected|Superseded)")
	} else if !contains([]string{"Proposed", "Accepted", "Rejected", "Superseded", "Decided"}, m[1]) {
		result.addError("status", "Invalid decision Status: "+m[1])
	}
	for _, section := range []string{"Context", "Decision", "Rationale", "Consequences"} {
		requireHeading(result, content, section)
	}
}

// requireHeading reports the section as missing unless the content carries a
// real `## Heading` line.
func requireHeading(result *ValidationResult, content, heading string) {
	pattern := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(heading) + `\s*$`)
	if !pattern.MatchString(content) {
		result.addError(strings.ToLower(strings.ReplaceAll(heading, " ", "_")), "Missing "+heading+" section")
	}
}

// decisionLinkPattern captures the two halves of a promotion link:
// `promoted-to:` on the deliberation log and `source:` on the ADR it was
// promoted into. `Supersedes:`/`Superseded by:` carry an ADR id, not a path,
// so there is nothing to resolve.
var decisionLinkPattern = regexp.MustCompile(`(?mi)^\s*-?\s*(source|promoted-to):\s*(\S.*?)\s*$`)

// validateDecisionLinks resolves promotion links against the repository root
// and requires the reverse half when a link is present. Solo ADRs with no
// source: (written directly, never promoted) stay valid.
func validateDecisionLinks(ctx context.Context, root, path, content string, result *ValidationResult) {
	self := normalizeDecisionPath(path)
	for _, match := range decisionLinkPattern.FindAllStringSubmatch(decisionHeaderBlock(content), -1) {
		field := strings.ToLower(match[1])
		target := strings.Trim(match[2], "`\"' ")
		if target == "" || target == "-" {
			continue
		}
		if !decisionFileExists(root, target) {
			result.addError(field, "Dangling "+field+" link (must be repo-root relative): "+target)
			continue
		}
		if canonicalFrozenZone(target) {
			continue
		}
		reverseField := "promoted-to"
		if field == "promoted-to" {
			reverseField = "source"
		}
		peer, err := decisionReadFile(root, target)
		if err != nil {
			result.addError(field, "Cannot read "+field+" target for reverse-link check: "+target)
			continue
		}
		if !decisionHeaderHasLink(peer, reverseField, self) {
			result.addError(field, fmt.Sprintf(
				"Missing reverse %s on %s (must point back to %s)",
				reverseField, target, self,
			))
		}
	}
}

func normalizeDecisionPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// decisionFileExists answers existence against the repository root, the way
// the reference storage resolves its repo-root-relative link targets.
func decisionFileExists(root, path string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil
}

func decisionReadFile(root, path string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// decisionHeaderHasLink reports whether the peer's header carries field
// pointing at expectedTarget (repo-root relative, slash-normalized).
func decisionHeaderHasLink(content, field, expectedTarget string) bool {
	want := normalizeDecisionPath(expectedTarget)
	for _, match := range decisionLinkPattern.FindAllStringSubmatch(decisionHeaderBlock(content), -1) {
		if strings.ToLower(match[1]) != field {
			continue
		}
		got := strings.Trim(match[2], "`\"' ")
		if got == "" || got == "-" {
			continue
		}
		if normalizeDecisionPath(got) == want {
			return true
		}
	}
	return false
}

// decisionHeaderBlock returns everything before the first `##` section.
// Promotion links live in the header (YAML frontmatter or the `- Status:`
// list), never in the prose — and Korean logs legitimately write bullets like
// "- source: 2026-07 감사 보고서" inside Rationale, which read as a link and
// would fail the document with a nonsense dangling error.
func decisionHeaderBlock(content string) string {
	if index := strings.Index(content, "\n## "); index >= 0 {
		return content[:index]
	}
	return content
}

// The decisions index is a hand-maintained catalog of documents that each
// carry the same facts in their own header. Nothing compared the two, so the
// index drifts one row at a time — every one of those drifts is invisible to
// the per-document validator, because each document is individually
// well-formed; only the pair disagrees. That makes this a corpus rule, and it
// is the reason the check hangs off ValidateAll rather than off a single
// document's validation.
//
// README.md is deliberately not a card (isNonCardFile skips it), so the
// findings cannot be attached to a card result. They get a synthetic result
// named after the README, which is the file a reader has to go edit.

// decisionIndexMaxPattern reads the "next number" line. Both halves are
// captured because a line that names the right maximum and the wrong
// successor is still a line the next author will copy.
var decisionIndexMaxPattern = regexp.MustCompile(`현재 최대: ADR-(\d+), 로그 (\d+)\. 다음은 ADR-(\d+), 로그 (\d+)\.`)

// decisionDatePattern reads the document's own `- Date:` the way
// decisionStatusPattern reads its Status, and accepts the same spellings.
var decisionDatePattern = regexp.MustCompile(`(?mi)^\s*-?\s*\*{0,2}Date\*{0,2}:\s*(\S+)`)

// reportDecisionIndexDrift compares the decisions README index against the
// documents it claims to catalog.
//
// Whether the check fires and what it concludes are two separate decisions,
// and keeping them separate is the point. It fires whenever the scan yielded
// live decision documents; from there, a missing README, a README with no
// index table, and an index table with no rows are findings, not reasons to
// stay quiet. Returning nil for those is what let the catalog be emptied,
// deleted, or renamed out of recognition with the gate green.
//
// It returns nil only when there is genuinely nothing to compare: a scan root
// that holds no decision documents at all, which is every default-tasks run.
func reportDecisionIndexDrift(root string, files []string, scanRoot string) *ValidationResult {
	docs := decisionCorpusFiles(files)
	if len(docs) == 0 {
		return nil
	}
	indexRoot := decisionIndexRoot(docs[0])
	if indexRoot == "" {
		return nil
	}
	readme := indexRoot + "/README.md"
	result := &ValidationResult{Path: readme}
	if !decisionFileExists(root, readme) {
		result.addError("index", fmt.Sprintf(
			"%d decision document(s) under %s/ and no index at %s", len(docs), indexRoot, readme))
		return result
	}
	content, err := decisionReadFile(root, readme)
	if err != nil {
		result.addError("index", "Cannot read decisions index: "+err.Error())
		return result
	}

	index := parseDecisionIndex(content, indexRoot)
	switch {
	case !index.sawHeader:
		result.addError("index", fmt.Sprintf(
			"%s holds no index table with a Status column; %d decision document(s) are catalogued by nothing",
			readme, len(docs)))
		return result
	case len(index.rows) == 0:
		result.addError("index", fmt.Sprintf(
			"%s has an index table with no rows; %d decision document(s) are catalogued by nothing",
			readme, len(docs)))
		return result
	}

	reportMissingIndexRows(docs, index.rows, result)
	for _, row := range index.rows {
		checkDecisionIndexRow(root, row, result)
	}
	// The next-number line speaks for the whole corpus, so it may only be
	// judged by a walk that saw the whole corpus.
	if scanCoversIndexRoot(scanRoot, indexRoot) {
		reportDecisionIndexMaxLine(content, docs, result)
	}
	return result
}

// scanCoversIndexRoot reports whether the walk that produced these documents
// swept the entire index root. An empty scanRoot means the caller did not
// say, which only in-package tests do; the CLI always says.
func scanCoversIndexRoot(scanRoot, indexRoot string) bool {
	if scanRoot == "" {
		return true
	}
	swept := filepath.ToSlash(filepath.Clean(scanRoot))
	if swept == "." {
		return true
	}
	return swept == indexRoot || strings.HasPrefix(indexRoot+"/", swept+"/")
}

// decisionCorpusFiles keeps the live decision documents out of the walk.
// Archived peers are history rather than maintained records, so the index is
// not required to list them.
func decisionCorpusFiles(files []string) []string {
	var docs []string
	for _, f := range files {
		if isDecisionDoc(f) && !canonicalFrozenZone(f) && !isNonCardFile(filepath.Base(f)) {
			docs = append(docs, filepath.ToSlash(f))
		}
	}
	return docs
}

// decisionIndexRoot returns the directory the index lives in: the first path
// segment that named this corpus a decision corpus. For decisions/adr/0033-x.md
// that is `decisions`, which is where the README sits — not `decisions/adr`.
func decisionIndexRoot(doc string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Dir(doc)), "/")
	for i, part := range parts {
		if part == "decision" || part == "decisions" || part == "adr" || part == "adrs" {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

// reportMissingIndexRows is drift kind (a): a document exists and the catalog
// never learned about it.
func reportMissingIndexRows(docs []string, rows []decisionIndexRow, result *ValidationResult) {
	listed := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		listed[row.target] = struct{}{}
	}
	for _, doc := range docs {
		if _, ok := listed[doc]; !ok {
			result.addError("index", "No index row for "+doc)
		}
	}
}

// checkDecisionIndexRow judges one row against the document it points at.
func checkDecisionIndexRow(root string, row decisionIndexRow, result *ValidationResult) {
	if !decisionFileExists(root, row.target) {
		result.addError("index", "Index row points at a missing file: "+row.target)
		return
	}
	content, err := decisionReadFile(root, row.target)
	if err != nil {
		result.addError("index", "Cannot read indexed document "+row.target+": "+err.Error())
		return
	}
	reportIndexStatusDrift(row, content, result)
	reportIndexDateDrift(row, content, result)
	reportIndexPromotionDrift(row, content, result)
}

// reportIndexStatusDrift compares the first word of each side. Both the row
// and the document qualify their Status in prose, and the qualification is
// not the claim.
func reportIndexStatusDrift(row decisionIndexRow, content string, result *ValidationResult) {
	match := decisionStatusPattern.FindStringSubmatch(content)
	if match == nil {
		return // validateDecisionDoc already reports a missing Status.
	}
	want := match[1]
	got := firstWord(row.status)
	if !strings.EqualFold(got, want) {
		result.addError("index", fmt.Sprintf(
			"Index row for %s says Status %q, the document says %q", row.target, got, want))
	}
}

// reportIndexDateDrift holds the fourth column to the same standard as the
// third. A document with no Date is silent here: validateDecisionDoc leaves
// Date advisory, and a check may not require through the index what it does
// not require of the document.
func reportIndexDateDrift(row decisionIndexRow, content string, result *ValidationResult) {
	if row.date == "" {
		return
	}
	match := decisionDatePattern.FindStringSubmatch(decisionHeaderBlock(content))
	if match == nil {
		return
	}
	want := match[1]
	got := firstWord(row.date)
	if got != want {
		result.addError("index", fmt.Sprintf(
			"Index row for %s says Date %q, the document says %q", row.target, got, want))
	}
}

// reportIndexPromotionDrift compares the promotion column with the promotion
// link the document actually carries. Which of the two contracts applies is
// decided by where the document sits, never by which table the row was typed
// into.
func reportIndexPromotionDrift(row decisionIndexRow, content string, result *ValidationResult) {
	if row.promoKind == "" {
		return
	}
	kind := decisionPromotionKindFor(row.target)
	if row.promoKind != kind {
		result.addError("index", fmt.Sprintf(
			"Index row for %s sits in a table that records %s, but that document's promotion link is %s",
			row.target, row.promoKind, kind))
		return
	}
	link := decisionHeaderLink(content, kind)
	if link == "" {
		if decisionPromotionClaimed(kind, row.promotion) {
			result.addError("index", fmt.Sprintf(
				"Index row for %s claims %s %q, the document carries none", row.target, kind, row.promotion))
		}
		return
	}
	want := decisionPromotionLabel(kind, link)
	if !strings.Contains(row.promotion, want) {
		result.addError("index", fmt.Sprintf(
			"Index row for %s says %s %q, the document points at %s", row.target, kind, row.promotion, want))
	}
}

// decisionPromotionKindFor derives the contract from the document's location:
// promoted records live under adr/, deliberation logs beside the README.
func decisionPromotionKindFor(doc string) string {
	if decisionDocIsADR(doc) {
		return "source"
	}
	return "promoted-to"
}

func decisionDocIsADR(doc string) bool {
	parent := filepath.Base(filepath.Dir(doc))
	return parent == "adr" || parent == "adrs"
}

// decisionPromotionClaimed reports whether the column says anything at all.
// The empty claim is spelled with an em dash, sometimes annotated.
func decisionPromotionClaimed(kind, cell string) bool {
	if kind == "source" {
		return strings.Contains(cell, ".md")
	}
	return strings.Contains(cell, "ADR-")
}

// decisionPromotionLabel is how the index spells a link target: a log cites
// the ADR by id, an ADR cites the log by filename.
func decisionPromotionLabel(kind, link string) string {
	base := filepath.Base(link)
	if kind == "promoted-to" {
		return "ADR-" + leadingDigits(base)
	}
	return base
}

func decisionHeaderLink(content, field string) string {
	for _, match := range decisionLinkPattern.FindAllStringSubmatch(decisionHeaderBlock(content), -1) {
		if !strings.EqualFold(match[1], field) {
			continue
		}
		target := strings.Trim(match[2], "`\"' ")
		if target == "" || target == "-" {
			continue
		}
		return target
	}
	return ""
}

// reportDecisionIndexMaxLine is drift kind (d). The line is the one place a
// future author looks before allocating a number, so a stale line hands out a
// number that is already taken.
func reportDecisionIndexMaxLine(content string, docs []string, result *ValidationResult) {
	maxADR, maxLog := decisionCorpusMaxima(docs)
	want := fmt.Sprintf("현재 최대: ADR-%04d, 로그 %03d. 다음은 ADR-%04d, 로그 %03d.",
		maxADR, maxLog, maxADR+1, maxLog+1)
	match := decisionIndexMaxPattern.FindStringSubmatch(content)
	if match == nil {
		result.addError("index", "Missing the next-number line ("+want+")")
		return
	}
	if match[0] != want {
		result.addError("index", fmt.Sprintf(
			"Next-number line says %q, the corpus says %q", match[0], want))
	}
}

// decisionCorpusMaxima separates the two number series by where the document
// sits: the promoted records live under adr/, the deliberation logs beside
// the README.
func decisionCorpusMaxima(docs []string) (int, int) {
	maxADR, maxLog := 0, 0
	for _, doc := range docs {
		n, err := strconv.Atoi(leadingDigits(filepath.Base(doc)))
		if err != nil {
			continue
		}
		if decisionDocIsADR(doc) {
			if n > maxADR {
				maxADR = n
			}
			continue
		}
		if n > maxLog {
			maxLog = n
		}
	}
	return maxADR, maxLog
}

func leadingDigits(s string) string {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return s[:end]
}

func firstWord(s string) string {
	fields := strings.Fields(strings.Trim(s, "* "))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// decisionIndexLinkPattern captures the first markdown link target in a table
// row.
var decisionIndexLinkPattern = regexp.MustCompile(`\[[^\]]*\]\(([^)]+\.md)\)`)

var decisionIndexSeparatorCell = regexp.MustCompile(`^:?-{2,}:?$`)

// decisionIndexRow is one body row of an index table, already resolved
// against the decisions root.
type decisionIndexRow struct {
	target    string // repo-root relative path the row links to
	status    string // contents of the Status column
	date      string // contents of the Date column
	promotion string // contents of the 승격 대상 / 원본 로그 column
	promoKind string // "promoted-to" for a log table, "source" for an ADR table
}

// decisionIndex is what the README turned out to hold. rows and sawHeader are
// reported separately on purpose: zero rows under a header is a catalog that
// lost its contents, and zero headers is a catalog that stopped being one.
type decisionIndex struct {
	rows      []decisionIndexRow
	sawHeader bool
}

// parseDecisionIndex reads every markdown table whose header carries a Status
// column. The columns are located by header name rather than by position: the
// log table and the ADR table put Status in different places, and a
// positional reader would have to be told which table it was in before it
// could read either.
func parseDecisionIndex(content, root string) decisionIndex {
	var index decisionIndex
	statusCol, dateCol, promoCol := -1, -1, -1
	promoKind := ""
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			statusCol, dateCol, promoCol, promoKind = -1, -1, -1, ""
			continue
		}
		cells := splitTableRow(trimmed)
		if isSeparatorRow(cells) {
			continue
		}
		if col := indexOfCell(cells, "status"); col >= 0 {
			index.sawHeader = true
			statusCol = col
			dateCol = indexOfCell(cells, "date")
			promoCol, promoKind = decisionPromotionColumn(cells)
			continue
		}
		if statusCol < 0 {
			continue
		}
		match := decisionIndexLinkPattern.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		row := decisionIndexRow{
			target:    filepath.ToSlash(filepath.Clean(root + "/" + match[1])),
			status:    cellAt(cells, statusCol),
			date:      cellAt(cells, dateCol),
			promotion: cellAt(cells, promoCol),
			promoKind: promoKind,
		}
		index.rows = append(index.rows, row)
	}
	return index
}

// decisionPromotionColumn names the promotion column by the direction it
// records: a log table cites the ADR a log was promoted into, an ADR table
// cites the log an ADR was written from.
func decisionPromotionColumn(cells []string) (int, string) {
	if c := indexOfCell(cells, "승격 대상"); c >= 0 {
		return c, "promoted-to"
	}
	if c := indexOfCell(cells, "원본 로그"); c >= 0 {
		return c, "source"
	}
	return -1, ""
}

func cellAt(cells []string, col int) string {
	if col < 0 || col >= len(cells) {
		return ""
	}
	return cells[col]
}

// splitTableRow splits a GFM table row on its unescaped delimiters and
// returns the cells with `\|` unescaped. Splitting on every `|` read an
// escaped pipe — the only way GFM can put a literal pipe inside a cell — as a
// column boundary, which shifted every cell after it by one.
func splitTableRow(line string) []string {
	var cells []string
	var cur strings.Builder
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case escaped:
			// A backslash only escapes a pipe here; anything else keeps both
			// characters, the way GFM renders them.
			if c != '|' {
				cur.WriteByte('\\')
			}
			cur.WriteByte(c)
			escaped = false
		case c == '\\':
			escaped = true
		case c == '|':
			cells = append(cells, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if escaped {
		cur.WriteByte('\\')
	}
	cells = append(cells, cur.String())

	// The row's outer pipes produce one empty cell at each end. Exactly one is
	// dropped per end: a genuinely empty first column (the corner cell of a
	// comparison table) is content, not punctuation.
	if len(cells) > 0 && strings.TrimSpace(cells[0]) == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && strings.TrimSpace(cells[len(cells)-1]) == "" {
		cells = cells[:len(cells)-1]
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

func isSeparatorRow(cells []string) bool {
	for _, cell := range cells {
		if !decisionIndexSeparatorCell.MatchString(cell) {
			return false
		}
	}
	return len(cells) > 0
}

func indexOfCell(cells []string, want string) int {
	for i, cell := range cells {
		if strings.EqualFold(strings.Trim(cell, "* "), want) {
			return i
		}
	}
	return -1
}
