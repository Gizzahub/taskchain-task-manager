package taskflow

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LintDirCensus is one tasks/ subdirectory's classification and card count.
type LintDirCensus struct {
	Name  string
	Kind  string
	Cards int
}

// LintIDParity is one card filed under a number its id does not name.
type LintIDParity struct {
	Path     string
	Filename int
	ID       int
}

// LintWarning is one advisory the board earns without failing lint.
type LintWarning struct {
	Code    string
	Path    string
	Message string
}

// LintCensus is the whole board's lint survey: where the cards sit, which
// cards are misplaced, and which advisories the placement earns. It carries
// everything both the CLEAN and the error renderings print, so the verdict
// logic exists once.
type LintCensus struct {
	Dirs             []LintDirCensus
	StrayCards       []string
	StructuralIssues []string
	StageDiagnostics []string
	IDParity         []LintIDParity
	HiddenWIP        []string
	Warnings         []LintWarning
	TotalCards       int
}

// ErrorCount is the census's error total, in the order the stderr census line
// names the categories.
func (c *LintCensus) ErrorCount() int {
	return len(c.StrayCards) + len(c.StructuralIssues) + len(c.StageDiagnostics) +
		len(c.IDParity) + len(c.HiddenWIP)
}

// Clean reports a board with no lint errors; warnings alone stay clean.
func (c *LintCensus) Clean() bool { return c.ErrorCount() == 0 }

// CensusLine is the one-line error census the refusal prints: every category,
// counted, so a caller can gate on the line it is told about.
func (c *LintCensus) CensusLine() string {
	return fmt.Sprintf("%d stray card(s); %d structural issue(s); %d stage diagnostic(s); %d id parity issue(s); %d hidden in progress",
		len(c.StrayCards), len(c.StructuralIssues), len(c.StageDiagnostics),
		len(c.IDParity), len(c.HiddenWIP))
}

// CollectLint surveys the live board once.
func CollectLint(root string) (*LintCensus, error) {
	cards, err := FindCards(root, true)
	if err != nil {
		return nil, err
	}
	census := &LintCensus{TotalCards: len(cards)}
	dirIndex := map[string]int{}
	structuralSeen := map[string]bool{}
	for _, card := range cards {
		segments := strings.Split(card.TasksRel, "/")
		if len(segments) == 1 {
			census.StrayCards = append(census.StrayCards, card.RepoRel())
		} else {
			dir := segments[0]
			if idx, ok := dirIndex[dir]; ok {
				census.Dirs[idx].Cards++
			} else {
				census.Dirs = append(census.Dirs, LintDirCensus{Name: dir, Kind: dirCensusKind(dir), Cards: 1})
				dirIndex[dir] = len(census.Dirs) - 1
			}
			if dirCensusKind(dir) == "?" && !structuralSeen[dir] {
				structuralSeen[dir] = true
				census.StructuralIssues = append(census.StructuralIssues,
					dir+"/ is neither a zone, a kind, nor storage")
			}
		}
		if filename, id, ok := idParityNumbers(card); ok && filename != id {
			census.IDParity = append(census.IDParity, LintIDParity{
				Path: card.RepoRel(), Filename: filename, ID: id,
			})
		}
		if card.Status == StatusInProgress && card.Zone != StatusInProgress.Dir() {
			census.HiddenWIP = append(census.HiddenWIP, card.RepoRel())
		}
		if card.Zone == StatusInProgress.Dir() && !cardHasActiveRun(root, card.ID) {
			census.Warnings = append(census.Warnings, LintWarning{
				Code: "doing-zone-exit",
				Path: card.RepoRel(),
				Message: fmt.Sprintf("card sits in doing/ but task %s has no active run in run-status states; "+
					"move the card on or restart the run", card.ID),
			})
		}
	}
	sort.Slice(census.Dirs, func(i, j int) bool { return census.Dirs[i].Name < census.Dirs[j].Name })
	sort.Strings(census.StrayCards)
	sort.Strings(census.HiddenWIP)
	return census, nil
}

// dirCensusKind classifies a tasks/ subdirectory for the DIRS table. A
// directory that is none of the three is a structural issue, shown as "?".
func dirCensusKind(dir string) string {
	if _, ok := StatusFromDir(dir); ok {
		return "zone"
	}
	if IsKindDir(dir) {
		return "kind"
	}
	if IsStorageDir(dir) {
		return "storage"
	}
	return "?"
}

// leadingFileNumberRe reads the number a card's file name leads with.
var leadingFileNumberRe = regexp.MustCompile(`^([0-9]+)`)

// idParityNumbers reads the filename's leading number and the id's trailing
// number, the pair lint compares. Files that lead with no number (plan
// spellings like P10-) are outside the comparison, not failing it.
func idParityNumbers(card *Card) (filename int, id int, ok bool) {
	m := leadingFileNumberRe.FindStringSubmatch(filepath.Base(card.TasksRel))
	if m == nil {
		return 0, 0, false
	}
	filename, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	arrow := strings.LastIndex(card.ID, "-")
	if arrow <= 0 {
		return 0, 0, false
	}
	id, err = strconv.Atoi(card.ID[arrow+1:])
	if err != nil {
		return 0, 0, false
	}
	return filename, id, true
}

// cardHasActiveRun reports whether the run ledger shows an active run for the
// card. The run lifecycle itself is outside this port; what the fixtures pin
// is the warning a doing/ card earns when no run speaks for it, so this probe
// reads the shared CE state directory and answers false when no run records
// exist at all.
func cardHasActiveRun(root, cardID string) bool {
	commonDir, ok := GitCommonDir(root)
	if !ok {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(commonDir, "ce", "runs"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(commonDir, "ce", "runs", entry.Name()))
		if err != nil || !strings.Contains(string(raw), cardID) {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if state, ok := strings.CutPrefix(line, "state:"); ok && isActiveRunState(strings.TrimSpace(state)) {
				return true
			}
		}
	}
	return false
}

func isActiveRunState(state string) bool {
	switch state {
	case "active", "running", "started":
		return true
	}
	return false
}

// RenderLintText writes the lint verdict. CLEAN names the board's directories
// and advisories; a failing census prints the error sections, the fix the
// caller can run, and the NOT DONE line every refusing verb closes with.
func RenderLintText(w io.Writer, census *LintCensus) {
	if census.Clean() {
		fmt.Fprintf(w, "TASK LINT — CLEAN (%d dir(s), %d card(s), every card where the board looks)\n",
			len(census.Dirs), census.TotalCards)
		if len(census.Dirs) > 0 {
			fmt.Fprintln(w)
			renderLintDirs(w, census.Dirs)
		}
		if len(census.Warnings) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "WARNINGS")
			for _, warning := range census.Warnings {
				fmt.Fprintf(w, "  %-25s%s: %s\n", warning.Code, warning.Path, warning.Message)
			}
		}
		return
	}
	fmt.Fprintf(w, "TASK LINT — %d STRAY(S), %d STRUCTURAL ISSUE(S)\n",
		len(census.StrayCards), len(census.StructuralIssues))
	fmt.Fprintln(w)
	if len(census.Dirs) > 0 {
		renderLintDirs(w, census.Dirs)
		fmt.Fprintln(w)
	}
	if len(census.StructuralIssues) > 0 {
		fmt.Fprintln(w, "STRUCTURAL ISSUES")
		for _, issue := range census.StructuralIssues {
			fmt.Fprintf(w, "  %s\n", issue)
		}
		fmt.Fprintln(w)
	}
	if n := len(census.IDParity); n > 0 {
		fmt.Fprintf(w, "ID PARITY (%d card(s) filed under a number their id does not name)\n", n)
		for _, parity := range census.IDParity {
			fmt.Fprintf(w, "  %-33sfilename %d vs id %d — the frontmatter id is authoritative, renumber the file to %d\n",
				parity.Path, parity.Filename, parity.ID, parity.ID)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "STRAYS")
	for _, stray := range census.StrayCards {
		fmt.Fprintf(w, "  %s\n", stray)
	}
	fmt.Fprintln(w)
	if len(census.HiddenWIP) > 0 {
		fmt.Fprintln(w, "HIDDEN IN PROGRESS")
		for _, path := range census.HiddenWIP {
			fmt.Fprintf(w, "  %s\n", path)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "NEXT")
	fmt.Fprintln(w, "  1. re-run `ce task lint` until it reports CLEAN")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "NOT DONE: nothing was moved or modified.")
}

func renderLintDirs(w io.Writer, dirs []LintDirCensus) {
	fmt.Fprintln(w, "DIRS")
	for _, dir := range dirs {
		fmt.Fprintf(w, "  %-17s%-23s%d\n", dir.Name+"/", dir.Kind, dir.Cards)
	}
}
