package taskflow

import (
	"fmt"
	"regexp"
	"strings"
)

// The binding-shape checks are static: they execute nothing and judge only
// whether a verify binding's own text can still go red. The port carries the
// two families the pinned reference (bb970b24) enforces whose bytes fixtures
// pin — an unbound checkbox, and a command substitution severed by `;` — and
// the checked-without-observation and multi-span warnings beside them.
//
// A binding this scan cannot finish reading (an unterminated quote or
// substitution) produces no finding: "no finding" is the only honest answer
// to a script the scan could not complete, so an unterminated span discards
// what was gathered so far rather than reporting a partial result.

var observedAtRe = regexp.MustCompile(`(?i)\bobserved:?\s+\d{4}-\d{2}-\d{2}\b`)

var placeholderValueRe = regexp.MustCompile(`^<[^>]+>$`)

// validateBindingShape runs the per-card static shape checks over the whole
// body: population first, then the per-binding human-fence and severed-
// substitution families.
func validateBindingShape(result *ValidationResult, body string) {
	enforceCriterionPopulation(result, body)
	for _, criterion := range criterionLines(body) {
		if misFencedHuman(criterion.Text) {
			result.Warnings = append(result.Warnings, Finding{"verify_human_fence",
				"human binding is wrapped in backticks; it is a human check, not a command"})
		}
		if criterion.Command == "" {
			continue
		}
		script := effectiveScript(criterion.Command)
		for _, snippet := range semicolonSeveredSubstitutions(script) {
			result.Errors = append(result.Errors, Finding{"binding_shape_semicolon", fmt.Sprintf(
				"verify binding severs a command substitution with ';', discarding its exit status so the binding cannot go red: %s",
				snippet)})
		}
	}
}

// enforceCriterionPopulation enforces the binding rule over the scanner's
// population: every checkbox needs a `| verify:` binding — a machine command
// or the `human — …` form — and an unbound checkbox is refused. One
// exemption: a checkbox whose entire text is an unfilled `<...>` placeholder
// is template residue, reported as a warning instead. A checked machine
// binding with no observation date stays a warning.
func enforceCriterionPopulation(result *ValidationResult, body string) {
	for _, line := range criterionLines(body) {
		rest, found := verifyBindingRest(line.Text)
		if !found {
			if placeholderValueRe.MatchString(strings.TrimSpace(line.Text)) {
				result.Warnings = append(result.Warnings, Finding{"criterion_placeholder",
					"criterion is an unfilled <...> placeholder: " + line.Text})
				continue
			}
			result.Errors = append(result.Errors, Finding{"verify_binding_missing",
				"checkbox has no verify binding: " + line.Text})
			continue
		}
		if humanBindingRe.MatchString(strings.TrimSpace(rest)) || misFencedHuman(line.Text) {
			continue
		}
		if line.Checked && !observedAtRe.MatchString(line.Text) {
			result.Warnings = append(result.Warnings, Finding{"verify_observed_at",
				"checked machine binding has no observed date: " + line.Text})
		}
		if backtickSpans(rest) > 1 {
			result.Warnings = append(result.Warnings, Finding{"verify_backtick_spans",
				"criterion has more than one backtick span after verify: " + line.Text})
		}
	}
}

func backtickSpans(s string) int {
	count := 0
	for {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			return count
		}
		_, end, ok := balancedCodeSpan(s[i:])
		if !ok {
			return count
		}
		count++
		s = s[i+end:]
	}
}

// misFencedHuman reports a human check written inside a code fence. The
// classification is still human; the fence is only how the author spelled it.
func misFencedHuman(criterion string) bool {
	rest, found := verifyBindingRest(criterion)
	if !found || !strings.HasPrefix(rest, "`") {
		return false
	}
	content, _, ok := balancedCodeSpan(rest)
	return ok && humanBindingRe.MatchString(strings.TrimSpace(content))
}

// semicolonSeveredSubstitutions returns, for each `IDENT=$(...)` assignment
// in script that is immediately followed (after only spaces or tabs) by a
// bare `;`, is not guarded by an earlier `set -e`, and whose captured value
// is never put through a check that can fail on it, the assignment text as
// evidence for the finding. Quoted and nested-substitution spans are skipped
// whole, so an assignment shape appearing only inside a string is never seen.
func semicolonSeveredSubstitutions(script string) []string {
	var found []string
	i := 0
	for i < len(script) {
		switch {
		case script[i] == '\'' || script[i] == '"':
			end, ok := shellSpanEnd(script, i)
			if !ok {
				return nil
			}
			i = end
		case script[i] == '$' && i+1 < len(script) && script[i+1] == '(':
			end, ok := shellSpanEnd(script, i)
			if !ok {
				return nil
			}
			if !isArithmeticExpansion(script, i) {
				if start, isAssign := assignmentStart(script, i); isAssign && !guardedBySetE(script, i) {
					j := end
					for j < len(script) && (script[j] == ' ' || script[j] == '\t') {
						j++
					}
					if j < len(script) && script[j] == ';' && (j+1 >= len(script) || script[j+1] != ';') {
						if rc, rest, preserved := statusCaptureFollows(script, j+1); preserved {
							if _, positive := consumerIsPositiveAssertion(script, rest, rc); positive {
								i = end
								continue
							}
						}
						ident := script[start : i-1]
						if matched, positive := consumerIsPositiveAssertion(script, j+1, ident); !matched || !positive {
							found = append(found, strings.TrimSpace(script[start:end]))
						}
					}
				}
			}
			i = end
		default:
			i++
		}
	}
	return found
}

// statusCaptureFollows reports an `rc=$?` (any identifier) written
// immediately after a semicolon. rest is the index just past that statement's
// separator, where a later assertion of rc can be read. The idiom keeps the
// substitution's exit status; it is not a severed capture.
func statusCaptureFollows(script string, afterSemi int) (ident string, rest int, ok bool) {
	i := afterSemi
	for i < len(script) && (script[i] == ' ' || script[i] == '\t') {
		i++
	}
	start := i
	if i >= len(script) || !isIdentByte(script[i]) || (script[i] >= '0' && script[i] <= '9') {
		return "", 0, false
	}
	i++
	for i < len(script) && isIdentByte(script[i]) {
		i++
	}
	if i+2 >= len(script) || script[i] != '=' || script[i+1] != '$' || script[i+2] != '?' {
		return "", 0, false
	}
	ident = script[start:i]
	i += 3
	for i < len(script) && (script[i] == ' ' || script[i] == '\t') {
		i++
	}
	if i >= len(script) || script[i] != ';' || (i+1 < len(script) && script[i+1] == ';') {
		return "", 0, false
	}
	return ident, i + 1, true
}

// leadingKeywordRe matches the one compound-command keyword a clause carrying
// an assignment may open with, as in `do n=$(...)`, without that keyword
// counting as the command word an assignment must otherwise start at.
var leadingKeywordRe = regexp.MustCompile(`^\s*(?:do|then|else|elif|while|until)\s+`)

// testWordRe, numericCompareRe and caseWordRe are the textual signals
// consumerIsPositiveAssertion looks for in a candidate consumer clause. A
// plain `grep -q PATTERN` on the captured value is deliberately NOT one of
// these signals, even when it is the script's last clause: an arbitrary
// fixed pattern carries no general guarantee of failing on the empty or
// wrong content a severed capture would leave behind.
var (
	testWordRe       = regexp.MustCompile(`(?:^|[;&|(\s])(?:test|\[)[\s(]`)
	numericCompareRe = regexp.MustCompile(`-(?:ge|gt|le|lt|eq|ne|n|z)\b`)
	caseWordRe       = regexp.MustCompile(`(?:^|[;&|(\s])case[\s(]`)
)

// clauseEnd returns the index of the next top-level `;` or newline at or
// after start, treating quotes, command substitutions, and brace groups as
// opaque so an inner `;` one of those carries is never mistaken for a clause
// boundary. A doubled `;;` — a case arm terminator — is skipped as a unit.
func clauseEnd(script string, start int) int {
	depth := 0
	i := start
	for i < len(script) {
		switch {
		case script[i] == '\'' || script[i] == '"' || (script[i] == '$' && i+1 < len(script) && script[i+1] == '('):
			end, ok := shellSpanEnd(script, i)
			if !ok {
				return len(script)
			}
			i = end
		case script[i] == '{':
			depth++
			i++
		case script[i] == '}':
			if depth > 0 {
				depth--
			}
			i++
		case script[i] == ';' && i+1 < len(script) && script[i+1] == ';':
			i += 2
		case depth == 0 && (script[i] == ';' || script[i] == '\n'):
			return i
		default:
			i++
		}
	}
	return len(script)
}

// clauseAssignment reports whether the clause spanning [start,end) is itself
// a bare `IDENT=$(...)` (optionally after one leading keyword), returning the
// assigned name and its `$(...)` span.
func clauseAssignment(script string, start, end int) (ident string, rhsStart, rhsEnd int, ok bool) {
	i := start
	if loc := leadingKeywordRe.FindStringIndex(script[start:end]); loc != nil {
		i = start + loc[1]
	}
	for i < end {
		switch {
		case script[i] == '$' && i+1 < end && script[i+1] == '(':
			spanEnd, spanOk := shellSpanEnd(script, i)
			if !spanOk || spanEnd > end {
				return "", 0, 0, false
			}
			s, isAssign := assignmentStart(script, i)
			if !isAssign {
				return "", 0, 0, false
			}
			return script[s : i-1], i, spanEnd, true
		case isIdentByte(script[i]) || script[i] == '=' || script[i] == ' ' || script[i] == '\t':
			i++
		default:
			return "", 0, 0, false
		}
	}
	return "", 0, 0, false
}

// identRefIndex returns the index of a `$ident` or `${ident` reference in
// text, honoring word boundaries so `$out` never matches ident "o" and
// `$outfile` never matches ident "out". -1 when absent.
func identRefIndex(text, ident string) int {
	for i := 0; i+1 < len(text); i++ {
		if text[i] != '$' {
			continue
		}
		rest := strings.TrimPrefix(text[i+1:], "{")
		if !strings.HasPrefix(rest, ident) {
			continue
		}
		if after := rest[len(ident):]; after != "" && isIdentByte(after[0]) {
			continue
		}
		return i
	}
	return -1
}

// cdConsumesIdent reports whether a clause `cd`s onto the (optionally
// quoted, optionally braced) value of ident — an empty or bogus captured
// path makes `cd` itself fail.
func cdConsumesIdent(clause, ident string) bool {
	re := regexp.MustCompile(`(?:^|[;&|(\s])cd\s+"?\$\{?` + regexp.QuoteMeta(ident) + `\b`)
	return re.MatchString(clause)
}

// isPositiveAssertionClause reports whether a clause can itself fail on the
// empty or wrong value a severed capture would leave behind: a `test`/`[`
// with a numeric or `-n`/`-z` comparison, a `case` statement, a `cd` onto
// the value, or anything gated by `|| exit`.
func isPositiveAssertionClause(clause, ident string) bool {
	switch {
	case testWordRe.MatchString(clause) && numericCompareRe.MatchString(clause):
		return true
	case caseWordRe.MatchString(clause) && strings.Contains(clause, "esac"):
		return true
	case cdConsumesIdent(clause, ident):
		return true
	}
	if idx := strings.LastIndex(clause, "||"); idx >= 0 {
		return strings.Contains(clause[idx:], "exit")
	}
	return false
}

// consumerIsPositiveAssertion walks the clauses of script starting at from,
// following a captured value from one `IDENT=$(...)` into the next when a
// clause merely re-captures it under a new name, until it finds a clause
// that actually consumes the value, or runs out of clauses or hops.
// matched is false when ident is never referenced again at all.
func consumerIsPositiveAssertion(script string, from int, ident string) (matched, positive bool) {
	pos := from
	for hop := 0; hop < 6 && pos < len(script); hop++ {
		for pos < len(script) {
			switch script[pos] {
			case ' ', '\t', '\n', ';':
				pos++
				continue
			}
			break
		}
		if pos >= len(script) {
			return false, false
		}
		end := clauseEnd(script, pos)
		clause := script[pos:end]
		if identRefIndex(clause, ident) < 0 {
			pos = end
			continue
		}
		if next, rhsStart, rhsEnd, ok := clauseAssignment(script, pos, end); ok {
			if identRefIndex(script[rhsStart:rhsEnd], ident) >= 0 {
				ident = next
				pos = end
				continue
			}
		}
		return true, isPositiveAssertionClause(clause, ident)
	}
	return false, false
}

// effectiveScript returns the text this validator should actually scan.
// Bindings written as `sh -c '<script>'` are unwrapped — the trap lives in
// the inner script sh parses fresh — but the unwrap is deliberately narrow:
// it fires only when the whole remainder after `sh -c ` is exactly one
// quoted argument with nothing trailing. Anything else falls back to the raw
// text.
var shDashCRe = regexp.MustCompile(`^(?:sh|bash|zsh|dash|ash)\s+-c\s+`)

func effectiveScript(command string) string {
	loc := shDashCRe.FindStringIndex(command)
	if loc == nil {
		return command
	}
	rest := command[loc[1]:]
	if rest == "" || (rest[0] != '\'' && rest[0] != '"') {
		return command
	}
	end, ok := shellSpanEnd(rest, 0)
	if !ok || strings.TrimSpace(rest[end:]) != "" {
		return command
	}
	return rest[1 : end-1]
}

// shellSpanEnd returns the index just past the quoted or command-substitution
// span that starts at s[i], where s[i] is `'`, `"`, or the two bytes `$(`.
// ok is false when the span never closes — the caller must stop rather than
// guess where the "real" script resumes.
func shellSpanEnd(s string, i int) (int, bool) {
	switch {
	case i >= len(s):
		return 0, false
	case s[i] == '\'':
		j := strings.IndexByte(s[i+1:], '\'')
		if j < 0 {
			return 0, false
		}
		return i + 1 + j + 1, true
	case s[i] == '"':
		k := i + 1
		for k < len(s) {
			switch {
			case s[k] == '\\':
				k += 2
			case s[k] == '"':
				return k + 1, true
			case s[k] == '$' && k+1 < len(s) && s[k+1] == '(':
				end, ok := shellSpanEnd(s, k)
				if !ok {
					return 0, false
				}
				k = end
			default:
				k++
			}
		}
		return 0, false
	case strings.HasPrefix(s[i:], "$("):
		depth := 1
		k := i + 2
		for k < len(s) {
			switch s[k] {
			case '\'', '"':
				end, ok := shellSpanEnd(s, k)
				if !ok {
					return 0, false
				}
				k = end
			case '(':
				depth++
				k++
			case ')':
				depth--
				k++
				if depth == 0 {
					return k, true
				}
			default:
				k++
			}
		}
		return 0, false
	default:
		return 0, false
	}
}

// setERe recognizes a `set` invocation carrying an errexit flag, as a short
// option (`-e`, `-eu`, …) or `-o errexit`. It is applied only to
// topLevelText's output, never the raw script, so text that merely mentions
// "set -e" inside an unrelated quoted string is never mistaken for the guard.
var setERe = regexp.MustCompile(`\bset\s+(?:-[A-Za-z]*e[A-Za-z]*\b|-o\s+errexit\b)`)

// topLevelText returns the live top-level text of script before end: every
// quoted or command-substitution span is blanked out, so a regex search
// across the result only ever matches live top-level statements.
func topLevelText(script string, end int) string {
	var b strings.Builder
	b.Grow(end)
	i := 0
	for i < end {
		if script[i] == '\'' || script[i] == '"' || (script[i] == '$' && i+1 < len(script) && script[i+1] == '(') {
			spanEnd, ok := shellSpanEnd(script, i)
			if !ok || spanEnd > end {
				break
			}
			b.WriteString(strings.Repeat(" ", spanEnd-i))
			i = spanEnd
			continue
		}
		b.WriteByte(script[i])
		i++
	}
	return b.String()
}

func guardedBySetE(script string, pos int) bool {
	return setERe.MatchString(topLevelText(script, pos))
}

// isArithmeticExpansion reports whether the span starting at s[i] is a
// `$((...))` arithmetic expansion rather than a `$(...)` command
// substitution: arithmetic spawns no process and discards no exit status.
func isArithmeticExpansion(s string, i int) bool {
	return strings.HasPrefix(s[i:], "$((")
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// assignmentStart reports the start of an `IDENT=` immediately preceding the
// `$(` at dollarIdx, requiring that IDENT itself begin at script start or
// after a command-word boundary — what tells a real assignment apart from a
// CLI flag that merely looks like one.
func assignmentStart(script string, dollarIdx int) (int, bool) {
	if dollarIdx == 0 || script[dollarIdx-1] != '=' {
		return 0, false
	}
	eq := dollarIdx - 1
	start := eq
	for start > 0 && isIdentByte(script[start-1]) {
		start--
	}
	if start == eq {
		return 0, false
	}
	if start > 0 {
		switch script[start-1] {
		case ' ', '\t', '\n', ';', '&', '|', '(', ')':
		default:
			return 0, false
		}
	}
	return start, true
}
