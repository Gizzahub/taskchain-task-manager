package taskflow

import (
	"path/filepath"
	"regexp"
	"strings"
)

// The zone-path citation gate rejects a live card's body citing a
// tasks/<zone>/<file>.md path whose filename does not exist anywhere under
// the board. The role split below is the pinned reference's (bb970b24): a
// fenced block is example output and excluded; inline code immediately after
// a `| verify:` marker is the command a gate actually runs and is included;
// any other inline code span is a human-facing example and excluded.
//
// The three-digit run in the citation filename is required, not incidental:
// it is what lets `NNN` and other non-numeric placeholders pass through
// unmatched with no allowlist or wording heuristic.

// canonicalDirNames lists every directory a task card can live in: the
// workflow zones, the kind zones, and both storage spellings.
func canonicalDirNames() []string {
	return []string{
		"doing", "todo", "review", "blocked", "done",
		"plan", "issue", "backlog",
		LegacyStorageDir, StorageWriteDir,
	}
}

var zonePathCitationRe = regexp.MustCompile(
	`\btasks/(?:` + strings.Join(canonicalDirNames(), "|") + `)/([A-Za-z0-9_-]*[0-9]{3}[A-Za-z0-9_-]*\.md)\b`,
)

// stripInlineCodeSpans removes every prose inline code span, runs of
// backticks and all, reading spans with balancedCodeSpan so a double-fenced
// span holding one is removed whole. An unterminated backtick run opens no
// span and is left in place.
func stripInlineCodeSpans(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '`' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if _, end, ok := balancedCodeSpan(s[i:]); ok {
			i += end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// verifyScopeMarkerRe matches a `| verify-scope:` metadata marker; the scope
// validator itself is not part of this port, but the citation gate's prose
// split must not read a scope value as criterion prose.
var verifyScopeMarkerRe = regexp.MustCompile(`(?i)\|\s*verify-scope\s*:`)

// withoutVerifyScopeMetadata removes every `| verify-scope: …` segment, up to
// the next pipe or end of line.
func withoutVerifyScopeMetadata(line string) string {
	for {
		loc := verifyScopeMarkerRe.FindStringIndex(line)
		if loc == nil {
			return line
		}
		end := len(line)
		if next := strings.IndexByte(line[loc[1]:], '|'); next >= 0 {
			end = loc[1] + next
		}
		line = line[:loc[0]] + line[end:]
	}
}

// verifyBindingSplit is the prose/verify role split of one body line. prose
// is markdown a person reads — its inline code spans are examples and are
// stripped before the gate looks. verify is the text a gate actually runs —
// kept intact, backticks and all.
type verifyBindingSplit struct {
	prose     string
	verify    string
	hasVerify bool
}

func splitVerifyBinding(line string) verifyBindingSplit {
	loc := findVerifyBindingToken(line)
	if loc == nil {
		return verifyBindingSplit{prose: stripInlineCodeSpans(withoutVerifyScopeMetadata(line))}
	}
	return verifyBindingSplit{
		prose:     stripInlineCodeSpans(withoutVerifyScopeMetadata(line[:loc[0]])),
		verify:    line[loc[0]:],
		hasVerify: true,
	}
}

// zonePathCitationsIn walks body one line at a time, in FenceScanner order
// (every line must reach Classify, or the scanner desyncs), and returns every
// tasks/<zone>/<file>.md citation found outside a fenced code block, applying
// the prose/verify role split to each surviving line.
func zonePathCitationsIn(body string) []string {
	var citations []string
	var fences FenceScanner
	for _, line := range strings.Split(body, "\n") {
		if fences.Classify(line) == InsideFence {
			continue
		}
		split := splitVerifyBinding(line)
		citations = append(citations, zonePathCitationRe.FindAllString(split.prose, -1)...)
		citations = append(citations, zonePathCitationRe.FindAllString(split.verify, -1)...)
	}
	return citations
}

// corpusIndex memoizes the board walk behind the citation gate: which card
// filenames exist anywhere under the tasks directory (storage included), and
// every location each filename was seen at. One O(tree) walk serves every
// card; the result cannot change within one validation run.
type corpusIndex struct {
	filenames map[string]struct{}
	paths     map[string][]string
}

func buildCorpusIndex(root string) *corpusIndex {
	index := &corpusIndex{
		filenames: map[string]struct{}{},
		paths:     map[string][]string{},
	}
	cards, err := FindCards(root, false)
	if err != nil {
		return index
	}
	for _, card := range cards {
		base := filepath.Base(card.TasksRel)
		index.filenames[base] = struct{}{}
		index.paths[base] = append(index.paths[base], card.RepoRel())
	}
	return index
}

// validateZonePathCitations checks one card's body citations against the
// board: an unresolved filename is an error, a citation naming a file that
// lives in a different zone than cited is a stale-zone warning.
func validateZonePathCitations(result *ValidationResult, card *Card, index *corpusIndex) {
	citations := zonePathCitationsIn(card.Body)
	if len(citations) == 0 {
		return
	}
	self := filepath.Base(card.TasksRel)
	seen := make(map[string]struct{}, len(citations))
	for _, citation := range citations {
		if _, dup := seen[citation]; dup {
			continue
		}
		seen[citation] = struct{}{}
		base := filepath.Base(citation)
		if _, ok := index.filenames[base]; !ok {
			result.Errors = append(result.Errors, Finding{"zone_path_citation",
				"Zone-path citation does not resolve on disk: " + citation +
					" -- an example path belongs in backticks, or uses a non-numeric placeholder such as NNN"})
			continue
		}
		if base == self {
			continue
		}
		if citationMatchesALocation(citation, index.paths[base]) {
			continue
		}
		result.Warnings = append(result.Warnings, Finding{"zone_path_stale",
			"citation names an old zone for " + base + ": " + citation})
	}
}

func citationMatchesALocation(citation string, locations []string) bool {
	want := citationZone(citation)
	for _, location := range locations {
		if citationZone(location) == want {
			return true
		}
	}
	return false
}

func citationZone(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if part == "tasks" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// countPathBindings sums the board's citation counter for one card: every
// command binding the scanner could even split is examined, everything else
// (shells, pipes, substitutions, globs) was unreadable without a shell.
// Readability here is the probe splitter only — the allowlist that gates
// execution does not apply to counting, and the pinned summary line reports
// how much of the board's verification was examinable, not executable.
func countPathBindings(criteria []Criterion) (examined, skipped int) {
	for _, criterion := range criteria {
		if criterion.Command == "" {
			continue
		}
		if _, err := splitProbeArgs(strings.TrimPrefix(strings.TrimSpace(criterion.Command), "! ")); err == nil {
			examined++
		} else {
			skipped++
		}
	}
	return examined, skipped
}

// pathBindings examines and skipped counts for one card, in the shape the
// validation summary adds up.
func pathBindings(criteria []Criterion) Citations {
	examined, skipped := countPathBindings(criteria)
	return Citations{Examined: examined, Skipped: skipped}
}
