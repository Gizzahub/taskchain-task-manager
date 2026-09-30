package taskflow

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Lint answers the question validate cannot: is every card somewhere a reader
// would look? Validate judges card documents; lint judges the filing — shadow
// zone spellings, cards at the tasks root, directories nobody declared, and
// cards whose filename number contradicts the id they declare. It moves
// nothing; the verdict is evidence for whoever tidies. Everything here mirrors
// the pinned reference (bb970b24) down to the render bytes the fixtures pin.
//
// The product has no card-dialect loader and no run ledger, so two CE inputs
// are constants here: no zone is ever declared (cards in kind directories are
// parked, nothing else is), and no task ever has an active run (the
// doing-zone-exit pass therefore warns for every doing/ card with a canonical
// id). Both are recorded divergences, not silent ones.

// LintStrayKind classifies why a card is invisible to the board.
type LintStrayKind string

const (
	LintStrayShadowZone    LintStrayKind = "shadow-zone"
	LintStrayUnknownDir    LintStrayKind = "unknown-dir"
	LintStrayRootCard      LintStrayKind = "root-card"
	LintStrayHiddenWIP     LintStrayKind = "hidden-wip"
	LintStrayResolvedIssue LintStrayKind = "resolved-issue"
)

// Lint dir verdicts: what each top-level directory under tasks/ is.
const (
	LintDirZone       = "zone"
	LintDirShadowZone = "shadow-zone"
	LintDirKind       = "kind"
	LintDirDeclared   = "declared"
	LintDirStorage    = "storage"
	LintDirAttachment = "attachment"
	LintDirUnknown    = "unknown"
)

// LintStray is one misfiled card and the place it should be.
type LintStray struct {
	Path    string
	Kind    LintStrayKind
	Dir     string
	Status  string
	Suggest string
}

// LintWarning is one repository-level finding. A warning never makes the
// report unclean: nothing is misfiled, so no card has to move.
type LintWarning struct {
	Kind   string
	Detail string
}

// LintStructureIssue is a task-tree shape that remains readable but is not
// part of the closed board layout. Unlike warnings it makes lint fail.
type LintStructureIssue struct {
	Path   string
	Detail string
}

// LintStageDiagnostic is one workflow-stage metadata violation. The product
// carries no card dialect, so no stage rule can fire; the type and the JSON
// key exist so the machine contract keeps CE's shape.
type LintStageDiagnostic struct {
	Path   string
	ID     string
	Field  string
	Reason string
}

// LintIDParityIssue is one card filed under a filename number its declared id
// does not name.
type LintIDParityIssue struct {
	Path     string
	Filename int
	Declared int
}

// LintDir is one top-level directory and what lint thinks it is.
type LintDir struct {
	Name      string
	Kind      string
	Canonical string
	Cards     int
}

// LintReport is the queue-hygiene verdict.
type LintReport struct {
	Strays           []LintStray
	Dirs             []LintDir
	Counts           map[LintStrayKind]int
	Warnings         []LintWarning
	Structure        []LintStructureIssue
	StageDiagnostics []LintStageDiagnostic
	IDParity         []LintIDParityIssue
	HiddenWIP        int
	Clean            bool
	DirsEnumerated   bool
}

// filenameCardNumber is the leading number a card filename is filed under;
// the optional trailing letter keeps `007a-` one number, not a malformed one.
var filenameCardNumber = regexp.MustCompile(`^(\d+)[a-z]?-`)

// Lint walks every card and reports the ones the board cannot see. It never
// mutates anything.
func Lint(root string) *LintReport {
	live, _ := FindCards(root, true)
	all, _ := FindCards(root, false)

	report := &LintReport{Counts: map[LintStrayKind]int{}}
	cardsPerDir := map[string]int{}
	for _, card := range all {
		if dir := firstTasksSegment(card.TasksRel); dir != "" {
			cardsPerDir[dir]++
		}
	}
	for _, card := range live {
		if stray, ok := classifyLintCard(card); ok {
			report.Strays = append(report.Strays, stray)
		}
	}

	// The dir census enumerates the real directory listing, not just the
	// directories that happen to hold a card: without it, "no todo/ in Dirs"
	// could not distinguish a drained queue from a layout that has none.
	names := map[string]bool{}
	tasksDir := filepath.Join(root, tasksDirName())
	if entries, err := os.ReadDir(tasksDir); err == nil {
		report.DirsEnumerated = true
		for _, entry := range entries {
			if entry.IsDir() {
				names[entry.Name()] = true
			}
		}
	}
	for name := range cardsPerDir {
		names[name] = true
	}
	for name := range names {
		verdict := classifyLintDir(name)
		verdict.Cards = cardsPerDir[name]
		report.Dirs = append(report.Dirs, verdict)
	}

	sort.Slice(report.Dirs, func(i, j int) bool { return report.Dirs[i].Name < report.Dirs[j].Name })
	sort.Slice(report.Strays, func(i, j int) bool { return report.Strays[i].Path < report.Strays[j].Path })
	for _, s := range report.Strays {
		report.Counts[s.Kind]++
	}
	report.HiddenWIP = report.Counts[LintStrayHiddenWIP]
	report.Warnings = append(report.Warnings, doingExitWarnings(live)...)
	report.Structure = lintStructureIssues(root)
	report.IDParity = lintIDParityIssues(live)
	report.Clean = len(report.Strays) == 0 && len(report.Structure) == 0 &&
		len(report.IDParity) == 0
	return report
}

// firstTasksSegment returns the directory segment directly under the tasks
// root for a tasks-relative card path, or "" for a root card.
func firstTasksSegment(tasksRel string) string {
	parts := strings.Split(filepath.ToSlash(tasksRel), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// classifyLintCard decides whether one card is a stray. A zone spelling
// anywhere in the path settles the card's home first; an in-progress claim is
// judged before parking, because a parked card that says it is being worked on
// is hidden WIP, not parked.
func classifyLintCard(card *Card) (LintStray, bool) {
	parts := strings.Split(filepath.ToSlash(card.TasksRel), "/")
	segs := parts[:len(parts)-1]
	for _, seg := range segs {
		st, ok := StatusFromDir(seg)
		if !ok {
			continue
		}
		if seg == st.Dir() {
			return LintStray{}, false
		}
		dir := ""
		if len(segs) > 0 {
			dir = segs[0]
		}
		return LintStray{Path: card.RepoRel(), Kind: LintStrayShadowZone, Dir: dir, Suggest: st.Dir()}, true
	}

	status := card.Status
	parked := false
	for _, seg := range segs {
		if IsKindDir(seg) {
			parked = true
			break
		}
	}

	stray := LintStray{
		Path:    card.RepoRel(),
		Status:  lintStatusClaim(status),
		Suggest: lintSuggestZone(status),
	}
	if len(segs) > 0 {
		stray.Dir = segs[0]
	}
	switch {
	case status == StatusInProgress:
		stray.Kind = LintStrayHiddenWIP
		stray.Suggest = StatusInProgress.Dir()
	case parked:
		return LintStray{}, false
	case len(segs) == 0:
		stray.Kind = LintStrayRootCard
	default:
		stray.Kind = LintStrayUnknownDir
	}
	return stray, true
}

// lintStatusClaim renders a status worth reporting. Pending is what a card
// gets for saying nothing, so it is not a claim.
func lintStatusClaim(status Status) string {
	if status == "" || status == StatusPending {
		return ""
	}
	return string(status)
}

// lintSuggestZone maps a status claim to the directory that claim names. A
// card that claims nothing gets no suggestion: guessing todo/ for it would put
// an unread idea onto the run queue.
func lintSuggestZone(status Status) string {
	if lintStatusClaim(status) == "" {
		return ""
	}
	return status.Dir()
}

// classifyLintDir names one top-level directory under tasks/.
func classifyLintDir(name string) LintDir {
	verdict := LintDir{Name: name}
	switch {
	case IsStorageDir(name):
		verdict.Kind = LintDirStorage
	case IsKindDir(name):
		verdict.Kind = LintDirKind
	case name == "evidence":
		verdict.Kind = LintDirAttachment
	default:
		st, ok := StatusFromDir(name)
		switch {
		case !ok:
			verdict.Kind = LintDirUnknown
		case name == st.Dir():
			verdict.Kind = LintDirZone
		default:
			verdict.Kind = LintDirShadowZone
			verdict.Canonical = st.Dir()
		}
	}
	return verdict
}

// isStoragePartition reports whether a segment directly below a storage
// directory names a partition of the archive rather than a stray directory:
// a zone or kind name (what `task archive` writes) or a YYYY-MM month.
func isStoragePartition(segment string) bool {
	if len(segment) == 7 && segment[4] == '-' {
		if _, err := strconv.Atoi(segment[:4]); err == nil {
			if _, err := strconv.Atoi(segment[5:]); err == nil {
				return true
			}
		}
	}
	if IsKindDir(segment) {
		return true
	}
	_, ok := StatusFromDir(segment)
	return ok
}

// lintIsCanonicalTaskDir reports whether a top-level name is one a writer may
// create: the workflow and kind zones plus both storage spellings.
func lintIsCanonicalTaskDir(name string) bool {
	if IsStorageDir(name) {
		return true
	}
	switch name {
	case "todo", "doing", "review", "blocked", "done", "issue", "plan", "backlog":
		return true
	}
	return false
}

// lintStructureIssues walks the tree and rejects the shapes the next writer
// cannot place deterministically: .gitkeep placeholders and directories
// outside the canonical layout.
func lintStructureIssues(root string) []LintStructureIssue {
	tasksDir := filepath.Join(root, tasksDirName())
	var issues []LintStructureIssue
	err := filepath.WalkDir(tasksDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(tasksDir, p)
		if relErr != nil || rel == "." {
			return nil
		}
		path := filepath.ToSlash(rel)
		if d.IsDir() {
			parts := strings.Split(path, "/")
			if len(parts) == 1 && lintIsCanonicalTaskDir(path) {
				return nil
			}
			if len(parts) > 1 && lintIsCanonicalTaskDir(parts[0]) {
				if parts[1] == "evidence" {
					return nil
				}
				if IsStorageDir(parts[0]) && isStoragePartition(parts[1]) {
					if len(parts) == 2 || parts[2] == "evidence" {
						return nil
					}
				}
			}
			issues = append(issues, LintStructureIssue{Path: path, Detail: fmt.Sprintf(
				"%q is not a canonical task directory; use one of: todo, doing, review, blocked, done, issue, plan, backlog, %s",
				path, StorageWriteDir)})
			return nil
		}
		if d.Name() == ".gitkeep" {
			issues = append(issues, LintStructureIssue{Path: path,
				Detail: ".gitkeep is prohibited under tasks; missing empty directories are normal"})
		}
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Path == issues[j].Path {
			return issues[i].Detail < issues[j].Detail
		}
		return issues[i].Path < issues[j].Path
	})
	return issues
}

// lintIsDecisionDoc reports whether a path is an ADR/decision document, the
// one card-shaped file series that claims no card id.
func lintIsDecisionDoc(path string) bool {
	dir := filepath.ToSlash(filepath.Dir(path))
	for _, part := range strings.Split(dir, "/") {
		if part == "decision" || part == "decisions" || part == "adr" || part == "adrs" {
			return true
		}
	}
	return false
}

// lintIDParityIssues reports one issue per live card whose filename number and
// declared id name different numbers. Numbers compare as numbers, so
// `007-x.md` declaring TASK-7 is one card under two spellings of one number.
func lintIDParityIssues(cards []*Card) []LintIDParityIssue {
	var out []LintIDParityIssue
	for _, card := range cards {
		path := card.RepoRel()
		if lintIsDecisionDoc(path) {
			continue
		}
		m := filenameCardNumber.FindStringSubmatch(filepath.Base(card.TasksRel))
		if m == nil {
			continue
		}
		named, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		_, declared, ok := parseCanonicalTaskID(strings.TrimSpace(card.ID))
		if !ok || declared == named {
			continue
		}
		out = append(out, LintIDParityIssue{Path: path, Filename: named, Declared: declared})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// doingExitWarnings reports cards parked in doing/ whose task has no active
// run, one warning per card. The product keeps no run ledger, so no card ever
// has one: the directory claims work is happening, and only a run would make
// that claim true.
func doingExitWarnings(cards []*Card) []LintWarning {
	var out []LintWarning
	for _, card := range cards {
		if !lintInCanonicalZone(card, StatusInProgress) {
			continue
		}
		if _, _, ok := parseCanonicalTaskID(strings.TrimSpace(card.ID)); !ok {
			continue
		}
		out = append(out, LintWarning{
			Kind: "doing-zone-exit",
			Detail: fmt.Sprintf("%s: card sits in doing/ but task %s has no active run in run-status states; move the card on or restart the run",
				card.RepoRel(), strings.TrimSpace(card.ID)),
		})
	}
	return out
}

// lintInCanonicalZone reports whether a card sits directly in the canonical
// directory of a status. Shadow spellings are deliberately excluded: classify
// already reports them as strays, and a second finding would repeat one
// misfiling twice on one card.
func lintInCanonicalZone(card *Card, status Status) bool {
	parts := strings.Split(filepath.ToSlash(card.TasksRel), "/")
	for _, seg := range parts[:len(parts)-1] {
		st, ok := StatusFromDir(seg)
		if ok && st == status && seg == st.Dir() {
			return true
		}
	}
	return false
}

// RenderLint writes the lint report in the fixture-pinned byte shape.
func RenderLint(w io.Writer, report *LintReport) {
	cards := 0
	for _, d := range report.Dirs {
		cards += d.Cards
	}
	if report.Clean {
		fmt.Fprintf(w, "TASK LINT — CLEAN (%d dir(s), %d card(s), every card where the board looks)\n",
			len(report.Dirs), cards)
	} else {
		fmt.Fprintf(w, "TASK LINT — %d STRAY(S), %d STRUCTURAL ISSUE(S)\n",
			len(report.Strays), len(report.Structure))
	}
	renderLintDirs(w, report)
	renderLintWarnings(w, report)
	renderLintStructure(w, report)
	renderLintStageDiagnostics(w, report)
	renderLintIDParity(w, report)
	if report.Clean {
		return
	}
	renderLintStrays(w, report)
	renderLintNext(w, report)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "NOT DONE: nothing was moved or modified.")
}

func renderLintDirs(w io.Writer, report *LintReport) {
	if len(report.Dirs) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "DIRS")
	for _, d := range report.Dirs {
		kind := d.Kind
		if d.Canonical != "" {
			kind = fmt.Sprintf("%s → %s/", d.Kind, d.Canonical)
		}
		fmt.Fprintf(w, "  %-16s %-22s %d\n", d.Name+"/", kind, d.Cards)
	}
}

func renderLintWarnings(w io.Writer, report *LintReport) {
	if len(report.Warnings) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "WARNINGS")
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "  %-24s %s\n", warning.Kind, warning.Detail)
	}
}

func renderLintStructure(w io.Writer, report *LintReport) {
	if len(report.Structure) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "STRUCTURE")
	for _, issue := range report.Structure {
		fmt.Fprintf(w, "  %-32s %s\n", issue.Path, issue.Detail)
	}
}

func renderLintStageDiagnostics(w io.Writer, report *LintReport) {
	if len(report.StageDiagnostics) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "STAGE DIAGNOSTICS (%d failing card metadata rule(s))\n", len(report.StageDiagnostics))
	for _, d := range report.StageDiagnostics {
		fmt.Fprintf(w, "  %-52s %s: %s\n", d.Path, d.Field, d.Reason)
	}
	fmt.Fprintln(w, "  → keep review verdict metadata only on finalized done/ cards")
}

func renderLintIDParity(w io.Writer, report *LintReport) {
	if len(report.IDParity) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "ID PARITY (%d card(s) filed under a number their id does not name)\n", len(report.IDParity))
	for _, issue := range report.IDParity {
		fmt.Fprintf(w, "  %-32s filename %d vs id %d — the frontmatter id is authoritative, renumber the file to %d\n",
			issue.Path, issue.Filename, issue.Declared, issue.Declared)
	}
}

func renderLintStrays(w io.Writer, report *LintReport) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "STRAYS")
	for _, s := range report.Strays {
		target := "?"
		if s.Suggest != "" {
			target = s.Suggest + "/"
		}
		line := fmt.Sprintf("  %-12s %s → %s", s.Kind, s.Path, target)
		if s.Status != "" {
			line += fmt.Sprintf("  (claims %s)", s.Status)
		}
		fmt.Fprintln(w, line)
	}
}

// renderLintNext orders the repairs by what they cost when skipped: hidden
// work first, because a run that starts over it duplicates it; shadow zones
// next, because every new card makes the split worse; the rest last.
func renderLintNext(w io.Writer, report *LintReport) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "NEXT")
	step := 1
	if report.HiddenWIP > 0 {
		fmt.Fprintf(w, "  %d. hidden WIP first: `ce task move <card> doing`, or fix the card's status if nobody is on it\n", step)
		step++
	}
	if report.Counts[LintStrayShadowZone] > 0 {
		fmt.Fprintf(w, "  %d. fold each shadow zone into its canonical directory: git mv tasks/<shadow>/* tasks/<canonical>/\n", step)
		step++
	}
	if report.Counts[LintStrayUnknownDir] > 0 || report.Counts[LintStrayRootCard] > 0 {
		fmt.Fprintf(w, "  %d. file unknown-dir and root cards into a zone\n", step)
		step++
	}
	fmt.Fprintf(w, "  %d. re-run `ce task lint` until it reports CLEAN\n", step)
}

// lintJSON is the machine contract, shared verbatim by `task lint --json` and
// the `strays` block of `preflight --json`, so a caller parses one shape.
type lintJSON struct {
	Clean            bool                      `json:"clean"`
	HiddenWIP        int                       `json:"hidden_wip"`
	Counts           map[string]int            `json:"counts"`
	Dirs             []lintDirJSON             `json:"dirs"`
	Cards            []lintCardJSON            `json:"cards"`
	Warnings         []lintWarningJSON         `json:"warnings"`
	Structure        []lintStructureJSON       `json:"structure"`
	StageDiagnostics []lintStageDiagnosticJSON `json:"stage_diagnostics"`
	IDParity         []lintIDParityJSON        `json:"id_parity"`
}

type lintIDParityJSON struct {
	Path     string `json:"path"`
	Filename int    `json:"filename"`
	Declared int    `json:"declared"`
}

type lintStageDiagnosticJSON struct {
	Path   string `json:"path"`
	ID     string `json:"id,omitempty"`
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type lintWarningJSON struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type lintStructureJSON struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

type lintDirJSON struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Canonical string `json:"canonical,omitempty"`
	Cards     int    `json:"cards"`
}

type lintCardJSON struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Dir     string `json:"dir,omitempty"`
	Status  string `json:"status,omitempty"`
	Suggest string `json:"suggest,omitempty"`
}

func lintPayload(report *LintReport) lintJSON {
	payload := lintJSON{
		Clean:            report.Clean,
		HiddenWIP:        report.HiddenWIP,
		Counts:           map[string]int{},
		Dirs:             make([]lintDirJSON, 0, len(report.Dirs)),
		Cards:            make([]lintCardJSON, 0, len(report.Strays)),
		Warnings:         make([]lintWarningJSON, 0, len(report.Warnings)),
		Structure:        make([]lintStructureJSON, 0, len(report.Structure)),
		StageDiagnostics: make([]lintStageDiagnosticJSON, 0, len(report.StageDiagnostics)),
		IDParity:         make([]lintIDParityJSON, 0, len(report.IDParity)),
	}
	for _, issue := range report.IDParity {
		payload.IDParity = append(payload.IDParity, lintIDParityJSON{Path: issue.Path, Filename: issue.Filename, Declared: issue.Declared})
	}
	for _, d := range report.StageDiagnostics {
		payload.StageDiagnostics = append(payload.StageDiagnostics, lintStageDiagnosticJSON{
			Path: d.Path, ID: d.ID, Field: d.Field, Reason: d.Reason,
		})
	}
	for _, warning := range report.Warnings {
		payload.Warnings = append(payload.Warnings, lintWarningJSON{Kind: warning.Kind, Detail: warning.Detail})
	}
	for _, issue := range report.Structure {
		payload.Structure = append(payload.Structure, lintStructureJSON{Path: issue.Path, Detail: issue.Detail})
	}
	for kind, n := range report.Counts {
		payload.Counts[string(kind)] = n
	}
	for _, d := range report.Dirs {
		payload.Dirs = append(payload.Dirs, lintDirJSON{Name: d.Name, Kind: d.Kind, Canonical: d.Canonical, Cards: d.Cards})
	}
	for _, s := range report.Strays {
		payload.Cards = append(payload.Cards, lintCardJSON{Path: s.Path, Kind: string(s.Kind), Dir: s.Dir, Status: s.Status, Suggest: s.Suggest})
	}
	return payload
}

// RenderLintJSON writes the machine-readable lint report.
func RenderLintJSON(w io.Writer, report *LintReport) error {
	data, err := json.MarshalIndent(lintPayload(report), "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

// LintFailureDetail is the stderr sentence a failing lint exits with, and the
// detail a failing gate lint step carries: five counts, one per repair class.
func LintFailureDetail(report *LintReport) string {
	return fmt.Sprintf("%d stray card(s); %d structural issue(s); %d stage diagnostic(s); %d id parity issue(s); %d hidden in progress",
		len(report.Strays), len(report.Structure), len(report.StageDiagnostics), len(report.IDParity), report.HiddenWIP)
}
