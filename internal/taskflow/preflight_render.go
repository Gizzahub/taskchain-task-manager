package taskflow

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// The preflight renderers: the text report and the machine verdict, in the
// byte shapes the parity fixtures pin. Text orders the report the way a
// reader scans it — the verdict line, what was scanned, strays the scope did
// not see, the runnable queue, then what blocks or merely degrades a run.

// preflightReasonHelp explains each verdict reason in the terms a reader can
// act on. The reason code alone names a category; the repair is what makes the
// report worth printing instead of a bare refusal.
var preflightReasonHelp = map[string]string{
	PreflightUnreadable:       "card could not be parsed",
	PreflightNoCriteria:       "no acceptance/completion criteria section",
	PreflightNoVerifyBinding:  "criteria carry no `| verify:` binding",
	PreflightPartialBinding:   "some criteria carry no `| verify:` binding",
	PreflightMalformedBinding: "a `| verify:` value is neither a backtick command nor `human — …`",
	PreflightNoExecTier:       "no exec-tier; falls back to standard",
	PreflightOversized:        "card is larger than the context budget",
}

// preflightRunnableListLimit caps the runnable cards printed in full. The
// count that was elided is always printed with it — a cap that hides its own
// truncation reads as "this is everything".
const preflightRunnableListLimit = 10

// RenderPreflightReport writes the text report. allowNoQueue renders the
// board-integration one-liner for a NO_QUEUE verdict; the standalone command
// prints the long form, because a caller that named no scope is asking the
// wrong tree rather than closing one.
func RenderPreflightReport(w io.Writer, report *PreflightReport, maxCardBytes int, allowNoQueue bool) {
	zones := "all"
	if len(report.Zones) > 0 {
		zones = strings.Join(report.Zones, ", ")
	}
	if maxCardBytes <= 0 {
		maxCardBytes = DefaultMaxCardBytes
	}
	dialect := "ce"

	fmt.Fprintf(w, "TASK PREFLIGHT — %s\n", report.Verdict)
	fmt.Fprintf(w, "zones: %s    dialect: %s    total %d    runnable %d    unrunnable %d\n",
		zones, dialect, report.Total, report.Runnable, report.Unrunnable)

	if report.Verdict == PreflightEmpty {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "No cards in scope. Nothing to run is not a failure.")
		renderPreflightStrays(w, report)
		return
	}

	if report.Verdict == PreflightNoQueue {
		if allowNoQueue {
			// The caller already knows this is board integration; the verdict
			// line above is the whole report.
			return
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "No queue in scope: %s.\n", preflightMissingZonesClause(report.MissingZones))
		fmt.Fprintln(w, "This is NOT a drained queue -- there is nowhere in scope for a card to be,")
		fmt.Fprintln(w, "so no card will appear here however much work the repository has.")
		return
	}

	renderPreflightStrays(w, report)
	renderPreflightRunnable(w, report)
	renderPreflightReasonGroup(w, "BLOCKING (cannot reach done as written)", report.Blocking, maxCardBytes)
	renderPreflightReasonGroup(w, "ADVISORY (degrades a run, does not stop it)", report.Advisory, maxCardBytes)
	renderPreflightNext(w, report)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "NOT DONE: no card was read past its metadata, moved, or modified.")
}

// preflightMissingZonesClause renders missing zones as the directories a
// reader can go look for, not as bare zone words, with their agreeing verb.
func preflightMissingZonesClause(zones []string) string {
	verb := "do not exist"
	if len(zones) == 1 {
		verb = "does not exist"
	}
	dirs := make([]string, 0, len(zones))
	for _, zone := range zones {
		dirs = append(dirs, tasksDirName()+"/"+zone+"/")
	}
	clause := "the scoped workflow zone(s)"
	if len(dirs) == 1 {
		clause = dirs[0]
	} else if len(dirs) > 1 {
		clause = strings.Join(dirs[:len(dirs)-1], ", ") + " and " + dirs[len(dirs)-1]
	}
	return clause + " " + verb
}

// renderPreflightStrays is the one place a zone-scoped verdict admits what it
// did not scan. It is printed before RUNNABLE because an EMPTY verdict over a
// tree with twelve strays is the report that most needs the next line.
func renderPreflightStrays(w io.Writer, report *PreflightReport) {
	lint := report.Lint
	if lint == nil || lint.Clean {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "STRAYS (%d card(s) the board does not see; `ce task lint` for the full list)\n", len(lint.Strays))
	if lint.HiddenWIP > 0 {
		fmt.Fprintf(w, "  %3d  %-12s claim in-progress outside doing/ -- STOP: file these before any run\n",
			lint.HiddenWIP, LintStrayHiddenWIP)
	}
	for _, kind := range []LintStrayKind{LintStrayShadowZone, LintStrayUnknownDir, LintStrayRootCard} {
		if n := lint.Counts[kind]; n > 0 {
			fmt.Fprintf(w, "  %3d  %-12s %s\n", n, kind, preflightStrayHelp(kind))
		}
	}
}

func preflightStrayHelp(kind LintStrayKind) string {
	switch kind {
	case LintStrayShadowZone:
		return "in a drift spelling of a zone (in_progress/, todos/); fold into the canonical dir"
	case LintStrayUnknownDir:
		return "in a directory CE has no word for; file into a zone or declare it in card-dialect.zones"
	case LintStrayRootCard:
		return "at the tasks root, in no zone at all"
	}
	return ""
}

func renderPreflightRunnable(w io.Writer, report *PreflightReport) {
	if report.Runnable == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "RUNNABLE")
	shown := 0
	for _, card := range report.Cards {
		if !card.Runnable {
			continue
		}
		if shown == preflightRunnableListLimit {
			fmt.Fprintf(w, "  ... and %d more runnable card(s) not listed\n", report.Runnable-shown)
			break
		}
		priority := card.Priority
		if priority == "" {
			priority = "--"
		}
		fmt.Fprintf(w, "  %-4s %s (%d/%d bound)\n", priority, card.Path, card.Bound, card.Criteria)
		shown++
	}
}

func renderPreflightReasonGroup(w io.Writer, heading string, counts map[string]int, maxCardBytes int) {
	if len(counts) == 0 {
		return
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	// Most-common first so the one fact that explains the queue leads.
	sort.Slice(reasons, func(i, j int) bool {
		if counts[reasons[i]] != counts[reasons[j]] {
			return counts[reasons[i]] > counts[reasons[j]]
		}
		return reasons[i] < reasons[j]
	})

	fmt.Fprintln(w)
	fmt.Fprintln(w, heading)
	for _, reason := range reasons {
		help := preflightReasonHelp[reason]
		if reason == PreflightOversized {
			help = fmt.Sprintf("%s (> %d bytes)", help, maxCardBytes)
		}
		// Padded to the longest reason code (case-mismatch-dependency, 24) so
		// the help column stays a column.
		fmt.Fprintf(w, "  %3d  %-24s %s\n", counts[reason], reason, help)
	}
}

func renderPreflightNext(w io.Writer, report *PreflightReport) {
	if report.Unrunnable == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "NEXT")
	step := 1
	if report.Blocking[PreflightNoCriteria] > 0 || report.Blocking[PreflightUnreadable] > 0 {
		fmt.Fprintf(w, "  %d. give every card a criteria section the reader can find\n", step)
		fmt.Fprintln(w, "     (## Acceptance Criteria | ## Completion Criteria | ## 완료 조건 | ## 완료 기준)")
		step++
	}
	if report.Blocking[PreflightNoVerifyBinding] > 0 || report.Blocking[PreflightPartialBinding] > 0 || report.Blocking[PreflightMalformedBinding] > 0 {
		fmt.Fprintf(w, "  %d. bind EVERY criterion, on the criterion's own line:\n", step)
		fmt.Fprintln(w, "     - [ ] <observable condition> | verify: `<command; exit 0 = pass>`")
		fmt.Fprintln(w, "     - [ ] <observable condition> | verify: human — <what a person checks>")
		step++
	}
	fmt.Fprintf(w, "  %d. re-run `ce task preflight` and only then start work\n", step)
}

// preflightJSON is the adapter-owned DTO for the machine contract.
type preflightJSON struct {
	Verdict      string              `json:"verdict"`
	Zones        []string            `json:"zones"`
	Total        int                 `json:"total"`
	Runnable     int                 `json:"runnable"`
	Unrunnable   int                 `json:"unrunnable"`
	Blocking     map[string]int      `json:"blocking"`
	Advisory     map[string]int      `json:"advisory"`
	Cards        []preflightCardJSON `json:"cards"`
	Strays       *lintJSON           `json:"strays,omitempty"`
	MissingZones []string            `json:"missing_zones,omitempty"`
}

type preflightCardJSON struct {
	Path     string   `json:"path"`
	Zone     string   `json:"zone,omitempty"`
	Title    string   `json:"title,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Runnable bool     `json:"runnable"`
	Blocking []string `json:"blocking"`
	Advisory []string `json:"advisory"`
	Criteria int      `json:"criteria"`
	Bound    int      `json:"bound"`
	Bytes    int      `json:"bytes"`
	ExecTier string   `json:"exec_tier,omitempty"`
}

// RenderPreflightJSON writes the machine-readable readiness verdict.
func RenderPreflightJSON(w io.Writer, report *PreflightReport) error {
	payload := preflightJSON{
		Verdict:      report.Verdict,
		Zones:        report.Zones,
		Total:        report.Total,
		Runnable:     report.Runnable,
		Unrunnable:   report.Unrunnable,
		Blocking:     report.Blocking,
		Advisory:     report.Advisory,
		Cards:        make([]preflightCardJSON, 0, len(report.Cards)),
		MissingZones: report.MissingZones,
	}
	if payload.Zones == nil {
		payload.Zones = []string{}
	}
	if payload.Blocking == nil {
		payload.Blocking = map[string]int{}
	}
	if payload.Advisory == nil {
		payload.Advisory = map[string]int{}
	}
	if report.Lint != nil {
		strays := lintPayload(report.Lint)
		payload.Strays = &strays
	}
	for _, card := range report.Cards {
		payload.Cards = append(payload.Cards, preflightCardJSON{
			Path:     card.Path,
			Zone:     card.Zone,
			Title:    card.Title,
			Priority: card.Priority,
			Runnable: card.Runnable,
			Blocking: preflightOrEmpty(card.Blocking),
			Advisory: preflightOrEmpty(card.Advisory),
			Criteria: card.Criteria,
			Bound:    card.Bound,
			Bytes:    card.Bytes,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

func preflightOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
