package taskflow

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Preflight answers one question before a run touches anything: can any card
// in the queue actually reach done? The completion gate behind it is fail-safe
// — an acceptance criterion with no `| verify:` binding is unverifiable, and
// unverifiable fails — so the same rule is applied up front, before a card
// body is read into a run. Everything here mirrors the pinned reference
// (bb970b24): the verdict vocabulary and the blocking/advisory split.
//
// Dependency gates (unmet/dangling/case-mismatch) are not part of this port:
// the product keeps no dependency corpus pass (recorded divergence), so a
// card's readiness rests on its own criteria, size, and exec tier.

// Preflight verdicts. Only BLOCKED is an error condition; an empty queue is a
// legitimate terminal state, not a failure.
const (
	PreflightReady   = "READY"
	PreflightBlocked = "BLOCKED"
	PreflightEmpty   = "EMPTY"
	// PreflightNoQueue is "this layout keeps no queue in scope", which EMPTY
	// would report as "the queue is drained". They are opposite instructions
	// to a caller: drained means the work is finished, no-queue means the
	// caller is asking the wrong tree and nothing it does next will find cards.
	PreflightNoQueue = "NO_QUEUE"
)

// Blocking reasons: the completion gate provably cannot pass.
const (
	PreflightUnreadable       = "unreadable"
	PreflightNoCriteria       = "no-criteria"
	PreflightNoVerifyBinding  = "no-verify-binding"
	PreflightPartialBinding   = "partial-binding"
	PreflightMalformedBinding = "malformed-binding"
)

// Advisory reasons: the card degrades a run but can still reach a verdict.
// Advisory reasons never make a queue BLOCKED.
const (
	PreflightNoExecTier = "no-exec-tier"
	PreflightOversized  = "oversized"
)

// DefaultMaxCardBytes is the size past which a card costs more context to read
// than the work it describes.
const DefaultMaxCardBytes = 20000

// PreflightCardReadiness is one card's verdict with the evidence behind it.
type PreflightCardReadiness struct {
	Path     string
	Zone     string
	Title    string
	Priority string
	Runnable bool
	Blocking []string
	Advisory []string
	// Criteria and Bound are the counts the blocking verdict derives from,
	// reported so a caller can see the ratio rather than trust the label.
	Criteria int
	Bound    int
	Bytes    int
}

// PreflightReport is the queue-level verdict. The aggregate is the point:
// judging cards one at a time hides an unrunnable backlog, while
// "runnable: 0" reads as the one fact that stops a run.
type PreflightReport struct {
	Verdict      string
	Zones        []string
	Total        int
	Runnable     int
	Unrunnable   int
	Cards        []PreflightCardReadiness
	Blocking     map[string]int
	Advisory     map[string]int
	MissingZones []string
	Lint         *LintReport
}

// PreflightOptions configures a readiness check.
type PreflightOptions struct {
	// Zones restricts the check to these workflow zones. Empty means every
	// workflow-zone card. Kind-zone cards (plan/issue/backlog) are never the
	// execution queue and are omitted unless a caller names that kind.
	Zones []string
	// MaxCardBytes is the advisory size ceiling; zero selects the default.
	MaxCardBytes int
}

// PreflightCheck reads the queue and returns its readiness without mutating
// anything.
func PreflightCheck(root string, opts PreflightOptions) *PreflightReport {
	if opts.MaxCardBytes <= 0 {
		opts.MaxCardBytes = DefaultMaxCardBytes
	}

	cards, _ := FindCards(root, true)
	sort.Slice(cards, func(i, j int) bool { return cards[i].RepoRel() < cards[j].RepoRel() })

	report := &PreflightReport{
		Zones:    opts.Zones,
		Blocking: map[string]int{},
		Advisory: map[string]int{},
	}
	for _, card := range cards {
		if kind := kindSegment(card.TasksRel); kind != "" {
			// Kind-zone cards are reference documents, not the run queue.
			if len(opts.Zones) == 0 || !preflightZoneSelected(kind, opts.Zones) {
				continue
			}
			report.Cards = append(report.Cards, preflightCheckCard(card, kind, opts.MaxCardBytes))
			continue
		}
		zone := pathZone(card.TasksRel)
		if !preflightZoneSelected(zone, opts.Zones) {
			continue
		}
		report.Cards = append(report.Cards, preflightCheckCard(card, zone, opts.MaxCardBytes))
	}

	for _, card := range report.Cards {
		report.Total++
		if card.Runnable {
			report.Runnable++
		} else {
			report.Unrunnable++
		}
		for _, r := range card.Blocking {
			report.Blocking[r]++
		}
		for _, r := range card.Advisory {
			report.Advisory[r]++
		}
	}

	report.Lint = Lint(root)
	if report.Total == 0 {
		if missing := preflightMissingWorkflowZones(opts.Zones, report.Lint); len(missing) > 0 {
			if !preflightHasDrainedArchiveEvidence(cards) {
				report.MissingZones = missing
			}
		}
	}

	switch {
	case report.Total == 0:
		if len(report.MissingZones) > 0 {
			report.Verdict = PreflightNoQueue
		} else {
			report.Verdict = PreflightEmpty
		}
	case report.Runnable == 0:
		report.Verdict = PreflightBlocked
	default:
		report.Verdict = PreflightReady
	}
	return report
}

// kindSegment returns the kind-zone segment of a tasks-relative path, or "".
func kindSegment(tasksRel string) string {
	parts := strings.Split(filepath.ToSlash(tasksRel), "/")
	for _, seg := range parts[:len(parts)-1] {
		if IsKindDir(seg) {
			return seg
		}
	}
	return ""
}

// pathZone derives the workflow zone from path segments rather than from the
// parsed card, so an unreadable card still lands in the zone the caller asked
// about instead of escaping the filter entirely.
func pathZone(tasksRel string) string {
	parts := strings.Split(filepath.ToSlash(tasksRel), "/")
	if _, status, ok := ZoneSegment(parts); ok {
		return status.Dir()
	}
	return ""
}

func preflightZoneSelected(zone string, zones []string) bool {
	if len(zones) == 0 {
		return true
	}
	for _, z := range zones {
		if strings.EqualFold(strings.TrimSpace(z), zone) {
			return true
		}
	}
	return false
}

// ValidatePreflightZones refuses a scope name that is not a workflow zone,
// kind zone, or declared parking directory. The product declares no parking
// directories, so the vocabulary here is CE's own.
func ValidatePreflightZones(zones []string) error {
	if len(zones) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, name := range canonicalDirNames() {
		known[name] = true
	}
	valid := append([]string{}, canonicalDirNames()...)
	for _, zone := range zones {
		name := strings.ToLower(strings.TrimSpace(zone))
		if name == "" {
			continue
		}
		if known[name] {
			continue
		}
		return fmt.Errorf("unknown zone %q (valid: %s)", zone, strings.Join(valid, ", "))
	}
	return nil
}

// preflightMissingWorkflowZones returns the scoped workflow zones when this
// tasks tree keeps no queue at all, or nil when it keeps one. The test is the
// WHOLE tree, not the scope: git does not track empty directories, so a
// repository that works its queue to the end has no todo/ in a fresh clone —
// judging the scope alone called that no-queue and refused runs on a queue
// that was merely finished.
func preflightMissingWorkflowZones(zones []string, lint *LintReport) []string {
	if lint == nil || !lint.DirsEnumerated {
		return nil
	}
	for _, dir := range lint.Dirs {
		if dir.Kind == LintDirZone {
			return nil
		}
	}
	return preflightScopedWorkflowZones(zones)
}

// preflightScopedWorkflowZones is the workflow zones a scope covers: the named
// ones, or every workflow zone when the scope is unrestricted. Kind and
// declared names are dropped — they are not queue directories.
func preflightScopedWorkflowZones(zones []string) []string {
	workflow := map[string]bool{"todo": true, "doing": true, "review": true, "blocked": true, "done": true}
	if len(zones) == 0 {
		return []string{"todo", "doing", "review", "blocked", "done"}
	}
	var scoped []string
	for _, zone := range zones {
		name := strings.ToLower(strings.TrimSpace(zone))
		if workflow[name] {
			scoped = append(scoped, name)
		}
	}
	return scoped
}

// preflightHasDrainedArchiveEvidence distinguishes an archive that records a
// completed workflow from an arbitrary storage directory: a canonical task
// card under storage/done proves the repository drained its workflow, and a
// live canonical work card defeats that conclusion.
func preflightHasDrainedArchiveEvidence(live []*Card) bool {
	for _, card := range live {
		kind, _, ok := parseCanonicalTaskID(strings.TrimSpace(card.ID))
		if ok && kind != canonicalPlan {
			return false
		}
	}
	all, _ := FindCards(".", false)
	for _, card := range all {
		if storageDirFromPath(card.RepoRel()) == "" || pathZone(card.TasksRel) != StatusDone.Dir() {
			continue
		}
		if kind, _, ok := parseCanonicalTaskID(strings.TrimSpace(card.ID)); ok && kind == canonicalTask {
			return true
		}
	}
	return false
}

// preflightCountCriteriaBindings reports how many criteria a card carries and
// how many of those have a usable verify binding. An orphan verify marker
// counts as malformed, so a binding that is not attached to a criterion cannot
// pass.
func preflightCountCriteriaBindings(body string, criteria []Criterion) (n, bound, malformed int) {
	n = len(criteria)
	if _, bodyLine, _, found := findCriteriaSection(body); found {
		if scan := scanCriteriaFrom(body, bodyLine); scan.OrphanVerifyMarker {
			malformed++
		}
	}
	for _, criterion := range criteria {
		switch {
		case verifyBindingValid(criterion.Text):
			bound++
		case hasVerifyBindingToken(criterion.Text):
			malformed++
		}
	}
	return n, bound, malformed
}

// preflightCheckCard scores one card against the work-card contract. The
// judgements about the file rather than the contract keep applying to kind
// cards: an unreadable card is still blocking and an oversized one is still
// advisory, but criteria bindings and exec tier belong to work cards only.
func preflightCheckCard(card *Card, zone string, maxCardBytes int) PreflightCardReadiness {
	readiness := PreflightCardReadiness{
		Path:     card.RepoRel(),
		Zone:     zone,
		Title:    card.Title,
		Priority: card.Priority,
		Bytes:    len(card.Raw),
	}
	if card.Zone != "" {
		readiness.Zone = card.Zone
	}
	if !IsKindDir(zone) {
		criteria := criterionLines(card.Body)
		n, bound, malformed := preflightCountCriteriaBindings(card.Body, criteria)
		readiness.Criteria, readiness.Bound = n, bound
		switch {
		case readiness.Criteria == 0:
			readiness.Blocking = append(readiness.Blocking, PreflightNoCriteria)
		case malformed > 0:
			readiness.Blocking = append(readiness.Blocking, PreflightMalformedBinding)
		case readiness.Bound == 0:
			readiness.Blocking = append(readiness.Blocking, PreflightNoVerifyBinding)
		case readiness.Bound < readiness.Criteria:
			// One unbound criterion is enough to fail the gate, so a partly
			// bound card is no more runnable than an unbound one — it just
			// looks it.
			readiness.Blocking = append(readiness.Blocking, PreflightPartialBinding)
		}
		if strings.TrimSpace(card.Frontmatter["exec-tier"]) == "" {
			readiness.Advisory = append(readiness.Advisory, PreflightNoExecTier)
		}
	} else {
		// An issue card's criteria are counted so a reader can see the
		// section, but the work-card binding gates do not apply: an issue is
		// not a runnable implementation unit.
		n, bound, _ := preflightCountCriteriaBindings(card.Body, criterionLines(card.Body))
		readiness.Criteria, readiness.Bound = n, bound
	}
	if maxCardBytes > 0 && readiness.Bytes > maxCardBytes {
		readiness.Advisory = append(readiness.Advisory, PreflightOversized)
	}
	readiness.Runnable = len(readiness.Blocking) == 0
	return readiness
}

// PreflightExitError is the standalone preflight command's failure reason,
// ordered by what stops a run first: hidden WIP outranks the queue verdict, a
// NO_QUEUE scope means the caller asked the wrong tree, and a BLOCKED queue
// means no card can reach done. The verdict was already rendered; this is the
// stderr sentence that goes with its exit code.
func PreflightExitError(report *PreflightReport) error {
	if report.Lint != nil && report.Lint.HiddenWIP > 0 {
		return fmt.Errorf("%d card(s) claim in-progress outside doing/; run `ce task lint` and file them before starting",
			report.Lint.HiddenWIP)
	}
	if report.Verdict == PreflightNoQueue {
		return fmt.Errorf("no queue in scope: %s", preflightMissingZonesClause(report.MissingZones))
	}
	if report.Verdict == PreflightBlocked {
		return fmt.Errorf("queue is not runnable: 0 of %d card(s) can reach done as written", report.Total)
	}
	return nil
}
