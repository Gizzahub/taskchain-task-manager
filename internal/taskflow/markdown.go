package taskflow

import (
	"path"
	"regexp"
	"strings"
)

// FenceScanner tracks fenced code block state across the lines of one file,
// in order. Only a bare closing run of the same character and at least the
// opening length closes a block; anything else inside is content.
type FenceScanner struct {
	char   byte
	length int
}

// FencePosition is a line's relation to the scanner's fence state.
type FencePosition int

// Fence positions.
const (
	OutsideFence FencePosition = iota
	InsideFence
	FenceDelimiter
)

// fenceRun measures the leading run of ``` or ~~~ on a line.
func fenceRun(line string) (char byte, length int, bare bool) {
	i := 0
	for i < len(line) && (line[i] == '`' || line[i] == '~') {
		i++
	}
	if i == 0 {
		return 0, 0, false
	}
	char = line[0]
	for j := 0; j < i; j++ {
		if line[j] != char {
			return 0, 0, false
		}
	}
	length = i
	bare = strings.TrimSpace(line[i:]) == ""
	return char, length, bare
}

// Classify advances the scanner with one line and reports where the line sits.
// Callers must feed every line in order: the scanner desyncs if a caller
// filters ahead of it.
func (f *FenceScanner) Classify(line string) FencePosition {
	char, run, bare := fenceRun(line)
	if f.length == 0 {
		if run == 0 {
			return OutsideFence
		}
		f.char, f.length = char, run
		return FenceDelimiter
	}
	if bare && char == f.char && run >= f.length {
		f.char, f.length = 0, 0
		return FenceDelimiter
	}
	return InsideFence
}

// MarkdownIndentedCode reports whether a line starts four or more columns in,
// which markdown reads as an indented code block. Tabs advance to the next
// multiple of four.
func MarkdownIndentedCode(line string) bool {
	cols := 0
	for cols < len(line) {
		switch line[cols] {
		case ' ':
			cols++
		case '\t':
			cols += 4 - cols%4
		default:
			return cols >= 4
		}
	}
	return cols >= 4
}

// balancedCodeSpan measures a backtick code span: it returns the span content
// and the index just past the closing run, matched against the opening run's
// length.
func balancedCodeSpan(s string) (content string, end int, ok bool) {
	if len(s) == 0 || s[0] != '`' {
		return "", 0, false
	}
	open := 0
	for open < len(s) && s[open] == '`' {
		open++
	}
	for i := open; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := 0
		for i+run < len(s) && s[i+run] == '`' {
			run++
		}
		if run == open {
			return s[open:i], i + run, true
		}
		i += run
	}
	return "", 0, false
}

// --- criteria scanning -------------------------------------------------------

var criterionCheckboxRe = regexp.MustCompile(`^-\s*\[([ xX>])\]\s+(.*)$`)

// Criterion is one checkbox line a validator grades.
type Criterion struct {
	Text    string
	Checked bool
	Command string
	Line    int
}

// criteriaScan collects the checkbox criteria of one body plus any verify
// marker that no checkbox owns.
type criteriaScan struct {
	Lines              []Criterion
	OrphanVerifyMarker bool
}

var verifyBindingTokenRe = regexp.MustCompile(`(?i)^\|\s*verify\s*:`)

// findVerifyBindingToken locates a `| verify:` marker outside code spans.
func findVerifyBindingToken(s string) []int {
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			if _, end, ok := balancedCodeSpan(s[i:]); ok {
				i += end - 1
			}
			continue
		}
		if s[i] != '|' {
			continue
		}
		if loc := verifyBindingTokenRe.FindStringIndex(s[i:]); loc != nil {
			return []int{i, i + loc[1]}
		}
	}
	return nil
}

func hasVerifyBindingToken(s string) bool { return findVerifyBindingToken(s) != nil }

var humanBindingRe = regexp.MustCompile(`(?i)^human\s+[—–-]\s+\S`)

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

func hasCommandCharacter(content string) bool {
	return strings.ContainsFunc(content, func(r rune) bool { return r != '`' && !isSpaceRune(r) })
}

// verifyBindingRest returns what follows the `| verify:` marker.
func verifyBindingRest(criterion string) (string, bool) {
	line := strings.TrimSpace(criterion)
	loc := findVerifyBindingToken(line)
	if loc == nil {
		return "", false
	}
	return strings.TrimLeft(line[loc[1]:], " \t"), true
}

// truncatedSingleFence detects the one shape a truncated file produces: a
// single-backtick code span that a later multi-backtick fence never closes.
func truncatedSingleFence(criterion string) bool {
	rest, found := verifyBindingRest(criterion)
	if !found || !strings.HasPrefix(rest, "`") || strings.HasPrefix(rest, "``") {
		return false
	}
	content, _, ok := balancedCodeSpan(rest)
	if !ok {
		return false
	}
	if strings.Contains(content, "\n") {
		return false
	}
	return strings.Contains(criterion, "\n``")
}

// verifyBindingCommand returns the backtick command of a verify binding.
func verifyBindingCommand(criterion string) (string, bool) {
	rest, found := verifyBindingRest(criterion)
	if !found || !strings.HasPrefix(rest, "`") {
		return "", false
	}
	content, _, ok := balancedCodeSpan(rest)
	if !ok || !hasCommandCharacter(content) {
		return "", false
	}
	if humanBindingRe.MatchString(strings.TrimSpace(content)) {
		return "", false
	}
	if truncatedSingleFence(criterion) {
		return "", false
	}
	return content, true
}

// verifyBindingValid reports whether the binding is a backtick command or a
// `human — …` phrase.
func verifyBindingValid(criterion string) bool {
	rest, found := verifyBindingRest(criterion)
	if !found {
		return false
	}
	if strings.HasPrefix(rest, "`") {
		content, _, ok := balancedCodeSpan(rest)
		if !ok || !hasCommandCharacter(content) || truncatedSingleFence(criterion) {
			return false
		}
		return true
	}
	return humanBindingRe.MatchString(rest)
}

// htmlCommentScanner strips rendered markdown HTML comments line by line while
// keeping code spans intact: a commented template is not a criterion, but a
// `printf '<!--'` command is code, not a comment opener.
type htmlCommentScanner struct {
	inComment bool
}

func (s *htmlCommentScanner) visible(line string) string {
	var visible strings.Builder
	remaining := line
	for remaining != "" {
		if s.inComment {
			end := strings.Index(remaining, "-->")
			if end < 0 {
				return visible.String()
			}
			s.inComment = false
			remaining = remaining[end+len("-->"):]
			continue
		}
		start := strings.Index(remaining, "<!--")
		codeStart := strings.IndexByte(remaining, '`')
		if codeStart >= 0 && (start < 0 || codeStart < start) {
			if _, end, ok := balancedCodeSpan(remaining[codeStart:]); ok {
				visible.WriteString(remaining[:codeStart+end])
				remaining = remaining[codeStart+end:]
				continue
			}
		}
		if start < 0 {
			visible.WriteString(remaining)
			break
		}
		visible.WriteString(remaining[:start])
		s.inComment = true
		remaining = remaining[start+len("<!--"):]
	}
	return visible.String()
}

var criteriaHeadingAliases = []string{
	"acceptance criteria",
	"completion criteria",
	"resolution criteria",
	"review criteria",
	"verification",
	"완료 조건",
	"완료 기준",
}

// IsCriteriaHeading reports whether a `## ` heading opens a criteria section.
func IsCriteriaHeading(line string) bool {
	lower := strings.ToLower(line)
	for _, alias := range criteriaHeadingAliases {
		if strings.Contains(lower, alias) {
			return true
		}
	}
	return false
}

func cardSectionHeading(line string) bool {
	return !MarkdownIndentedCode(line) && strings.HasPrefix(strings.TrimSpace(line), "## ")
}

// findCriteriaSection locates the first top-level criteria heading outside
// fences and indented code, and returns the section body plus the document
// line number of the body's first line.
func findCriteriaSection(document string) (section string, bodyLine int, found bool) {
	var body strings.Builder
	var fences FenceScanner
	for i, raw := range strings.Split(document, "\n") {
		if fences.Classify(raw) != OutsideFence {
			continue
		}
		if cardSectionHeading(raw) {
			if found {
				return body.String(), bodyLine, true
			}
			if IsCriteriaHeading(strings.TrimSpace(raw)) {
				found = true
				bodyLine = i + 2
			}
			continue
		}
		if found {
			body.WriteString(raw)
			body.WriteByte('\n')
		}
	}
	return body.String(), bodyLine, found
}

// scanCriteriaFrom walks a body collecting column-zero, unfenced,
// comment-stripped checkbox criteria, starting at the given document line.
func scanCriteriaFrom(body string, startLine int) criteriaScan {
	var scan criteriaScan
	var fences FenceScanner
	var comments htmlCommentScanner
	for i, raw := range strings.Split(body, "\n") {
		lineNo := startLine + i
		if comments.inComment {
			visible := comments.visible(raw)
			if fences.Classify(visible) == InsideFence {
				continue
			}
			scanVisibleCriteriaLine(&scan, visible, lineNo)
			continue
		}
		if fences.Classify(raw) != OutsideFence {
			continue
		}
		visible := comments.visible(raw)
		scanVisibleCriteriaLine(&scan, visible, lineNo)
	}
	return scan
}

func scanVisibleCriteriaLine(scan *criteriaScan, visible string, lineNo int) {
	indented := len(visible) > 0 && (visible[0] == ' ' || visible[0] == '\t')
	trimmed := strings.TrimSpace(visible)
	m := criterionCheckboxRe.FindStringSubmatch(trimmed)
	if m == nil {
		if hasVerifyBindingToken(trimmed) {
			scan.OrphanVerifyMarker = true
		}
		return
	}
	if indented {
		return
	}
	line := Criterion{Text: strings.TrimSpace(m[2]), Checked: m[1] == "x" || m[1] == "X", Line: lineNo}
	if command, ok := verifyBindingCommand(line.Text); ok {
		line.Command = command
	}
	scan.Lines = append(scan.Lines, line)
}

// criterionLines extracts the graded checkbox population: the first criteria
// section's checkboxes, or the whole body when no `## ` heading exists.
func criterionLines(body string) []Criterion {
	if documentHasHeading(body) {
		section, bodyLine, _ := findCriteriaSection(body)
		if bodyLine == 0 {
			bodyLine = 1
		}
		return scanCriteriaFrom(section, bodyLine).Lines
	}
	return scanCriteriaFrom(body, 1).Lines
}

func documentHasHeading(body string) bool {
	for _, raw := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(raw), "## ") {
			return true
		}
	}
	return false
}

// --- links -------------------------------------------------------------------

var markdownLinkRe = regexp.MustCompile(`(!?)\[([^\]]*)\]\(([^)]+)\)`)

type parsedLink struct {
	Original string
	Bang     string
	Text     string
	Dest     string
	// Escaped marks a destination that used %XX escapes; rewriting restores
	// them rather than writing the unescaped spelling.
	Escaped bool
	Start   int
	End     int
}

// parseLinkDestination reads a markdown link destination: relative only, with
// angle-bracket, title, and anchor forms tolerated.
func parseLinkDestination(dest string) (target string, escaped bool, ok bool) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", false, false
	}
	if strings.HasPrefix(dest, "<") {
		end := strings.Index(dest, ">")
		if end < 0 {
			return "", false, false
		}
		dest = dest[1:end]
	} else if i := strings.IndexAny(dest, " \t"); i >= 0 {
		// A title follows a space; the target is what came before it.
		dest = dest[:i]
	}
	if i := strings.Index(dest, "#"); i > 0 {
		dest = dest[:i]
	}
	if dest == "" {
		return "", false, false
	}
	if strings.Contains(dest, "\\") {
		return "", false, false
	}
	escaped = strings.Contains(dest, "%")
	if escaped {
		unescaped, err := pathUnescape(dest)
		if err != nil {
			return "", false, false
		}
		dest = unescaped
	}
	if strings.HasPrefix(dest, "/") || isURIScheme(dest) {
		return "", false, false
	}
	return dest, escaped, true
}

func pathUnescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func isURIScheme(dest string) bool {
	for i := 0; i < len(dest); i++ {
		if dest[i] == ':' {
			return i > 0
		}
		if !isAlpha(dest[i]) && !(i > 0 && isDigitOrPlus(dest[i])) {
			return false
		}
	}
	return false
}

func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isDigitOrPlus(c byte) bool {
	return c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

// escapePath percent-encodes the bytes markdown links cannot carry literally.
func escapePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c <= ' ' || c > '~' || c == '%' {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// rewriteRelativeLinks rewrites every relative link in content that resolves
// from docDir to matchResolved, writing newLiteral in its place. Matching is
// on resolved destinations, not literal text: a document is free to spell the
// same target with any amount of ../ drift, and all spellings must retarget.
func rewriteRelativeLinks(content, docDir, matchResolved, newLiteral string) (string, []LinkRewrite, error) {
	var out strings.Builder
	var rewrites []LinkRewrite
	var fences FenceScanner
	// Reconstructed exactly, separators included: the result carries the
	// content's own trailing-newline shape, so the caller never has to guess
	// whether a byte was gained or lost.
	for i, raw := range strings.Split(content, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		if fences.Classify(raw) != OutsideFence {
			out.WriteString(raw)
			continue
		}
		rewritten, lines := rewriteLinksOnLine(raw, i+1, docDir, matchResolved, newLiteral)
		rewrites = append(rewrites, lines...)
		out.WriteString(rewritten)
	}
	return out.String(), rewrites, nil
}

func rewriteLinksOnLine(line string, lineNo int, docDir, matchResolved, newLiteral string) (string, []LinkRewrite) {
	var rewrites []LinkRewrite
	var b strings.Builder
	for i := 0; i < len(line); {
		m := markdownLinkRe.FindStringSubmatchIndex(line[i:])
		if m == nil {
			b.WriteString(line[i:])
			break
		}
		start, end := i+m[0], i+m[1]
		bang := line[i+m[2] : i+m[3]]
		text := line[i+m[4] : i+m[5]]
		rawDest := line[i+m[6] : i+m[7]]
		target, escaped, ok := parseLinkDestination(rawDest)
		if ok && path.Join(docDir, target) == matchResolved {
			newDest := newLiteral
			if escaped {
				newDest = escapePath(newLiteral)
			}
			b.WriteString(line[i:start])
			b.WriteString(bang + "[" + text + "](" + newDest + ")")
			rewrites = append(rewrites, LinkRewrite{Line: lineNo, From: target, To: newDest})
			i = end
			continue
		}
		b.WriteString(line[i:end])
		i = end
	}
	return b.String(), rewrites
}
