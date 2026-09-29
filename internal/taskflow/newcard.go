package taskflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

// ErrNewCardInput marks a refusal caused by what the caller asked for, as
// opposed to a failure writing the file.
var ErrNewCardInput = errors.New("new card: invalid input")

// TaskTypes is the default work-card type vocabulary.
var TaskTypes = []string{"feature", "bug", "chore", "refactor", "cleanup", "docs", "test"}

// PriorityValues is the default priority vocabulary.
var PriorityValues = []string{"P0", "P1", "P2", "P3"}

var (
	slugRe       = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	slugLetterRe = regexp.MustCompile(`[a-z]`)
	nonSlugRe    = regexp.MustCompile(`[^a-z0-9]+`)
)

// NewCardOptions describes the card to create. Kind and Title are required.
type NewCardOptions struct {
	Kind     string
	ID       string
	Title    string
	Slug     string
	Type     string
	Priority string
	Effort   string
	ExecTier string
	Agent    string
	// Created is the YYYY-MM-DD stamp; the caller supplies it so the creator
	// stays deterministic.
	Created  string
	Criteria []string
	Root     string
}

// NewCardResult is where the card landed and what it is.
type NewCardResult struct {
	Path    string
	ID      string
	Kind    string
	Content string
}

// Create writes the card and returns where it went. It refuses before touching
// the filesystem when the request cannot yield a card the board would see --
// and the reservation ledger is part of that filesystem, which is why the id
// is taken after every input refusal rather than before.
func Create(ctx context.Context, opts NewCardOptions) (*NewCardResult, error) {
	if strings.TrimSpace(opts.Title) == "" {
		return nil, fmt.Errorf("%w: --title is required", ErrNewCardInput)
	}
	kind, err := resolveKind(opts.Kind)
	if err != nil {
		return nil, err
	}
	zone := zoneForKind(kind)
	prefix, err := PrefixForKind(kind)
	if err != nil {
		return nil, err
	}

	// checkID reads a value the caller supplied and writes nothing, so it
	// belongs with the other input refusals rather than with the reservation
	// below: a request wrong in two ways at once still names the id first.
	explicitID := strings.TrimSpace(opts.ID)
	if explicitID != "" {
		if err := checkID(explicitID, prefix); err != nil {
			return nil, err
		}
	}

	slug := strings.TrimSpace(opts.Slug)
	explicitSlug := slug != ""
	if !explicitSlug {
		slug = slugify(opts.Title)
	}
	// Shape alone lets a Korean title's stray digits stand as the whole slug,
	// because [a-z0-9]+ counts digits as slug material. A slug carries its
	// title's meaning in its letters; digits-only is the residue of a title
	// this function cannot read, which is what --slug exists for.
	switch {
	case !slugRe.MatchString(slug):
		return nil, fmt.Errorf("%w: slug %q is not lowercase-kebab; a title with no ASCII letters needs --slug", ErrNewCardInput, slug)
	case !slugLetterRe.MatchString(slug):
		return nil, fmt.Errorf("%w: slug %q carries no ASCII letter -- digits left over from the title are not a slug; a title with no ASCII letters needs --slug", ErrNewCardInput, slug)
	}
	if !explicitSlug && hasNonASCIITitleWord(opts.Title) {
		return nil, fmt.Errorf("%w: automatic slug %q would discard non-ASCII letters or digits from --title; pass --slug with a complete lowercase-kebab name", ErrNewCardInput, slug)
	}
	fields, err := resolveFields(kind, opts)
	if err != nil {
		return nil, err
	}
	for _, criterion := range opts.Criteria {
		if !verifyBindingValid(criterion) {
			return nil, fmt.Errorf("%w: criterion %q needs a backtick command or `human — …` after | verify", ErrNewCardInput, criterion)
		}
	}

	// The id is taken LAST because taking it is a WRITE: the reservation
	// ledger remembers the number whether or not a card follows it, so every
	// refusal above this line must stay free.
	id := explicitID
	if id == "" {
		id, err = allocateID(ctx, opts.Root, prefix)
		if err != nil {
			return nil, err
		}
	}
	number, _ := strconv.Atoi(strings.TrimPrefix(id, prefix+"-"))
	filename := fmt.Sprintf("%03d-%s.md", number, slug)
	// The ledger refuses its own overflow, but an explicit --id and a
	// repository-less tree scan can still name a number the filename pattern
	// cannot spell; none of them reaches the board.
	if number > cardid.MaxCardNumber {
		return nil, fmt.Errorf("%w: filename %q does not match the card filename pattern", ErrNewCardInput, filename)
	}
	content := render(kind, id, fields, opts)
	cardPath := filepath.Join(TasksDir, zone, filename)
	if err := writeNewFile(filepath.Join(opts.Root, cardPath), content); err != nil {
		return nil, err
	}
	return &NewCardResult{Path: filepath.ToSlash(cardPath), ID: id, Kind: kind, Content: content}, nil
}

func resolveKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "", fmt.Errorf("%w: kind is required (valid: task, issue, plan, backlog)", ErrNewCardInput)
	}
	switch kind {
	case "task", "issue", "plan", "backlog":
		return kind, nil
	}
	if st, ok := StatusFromDir(kind); ok {
		return "", fmt.Errorf(
			"%w: %q is a zone name, not a kind -- task new takes a kind (valid: task, issue, plan, backlog); a task card is created as `task` and lands in %s/, and %s/ is reached with task move",
			ErrNewCardInput, kind, StatusPending.Dir(), st.Dir())
	}
	if IsStorageDir(kind) {
		return "", fmt.Errorf("%w: %s/ is long-term storage, not a kind (valid: task, issue, plan, backlog)", ErrNewCardInput, kind)
	}
	return "", fmt.Errorf("%w: unknown kind %q (valid: task, issue, plan, backlog)", ErrNewCardInput, kind)
}

func zoneForKind(kind string) string {
	if IsKindDir(kind) {
		return kind
	}
	return StatusPending.Dir()
}

func PrefixForKind(kind string) (string, error) {
	switch kind {
	case "task":
		return "TASK", nil
	case "plan":
		return "PLAN", nil
	case "issue":
		return "ISSUE", nil
	case "backlog":
		return "BACKLOG", nil
	}
	return "", fmt.Errorf("unsupported card kind %q", kind)
}

// checkID validates an explicit --id through the canonical card-id grammar.
// The prefix must still be the kind's own: an id is not merely well-formed,
// it must name a card of the kind being created.
func checkID(id, prefix string) error {
	parsed, err := cardid.Parse(id)
	if err != nil || parsed.Prefix != prefix {
		return fmt.Errorf("%w: --id %q must look like %s-N", ErrNewCardInput, id, prefix)
	}
	return nil
}

func slugify(title string) string {
	slug := nonSlugRe.ReplaceAllString(strings.ToLower(title), "-")
	return strings.Trim(slug, "-")
}

// hasNonASCIITitleWord identifies title text slugify cannot represent: a
// non-ASCII letter or digit carries title meaning an ASCII-only filename
// would silently lose.
func hasNonASCIITitleWord(title string) bool {
	for _, r := range title {
		if r > unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return true
		}
	}
	return false
}

// allocateID takes the next number through the shared reservation ledger when
// a repository exists, falling back to its own tree scan when it does not.
// The floor is cardid's frontmatter-only scan; the history scan it is maxed
// with stays this package's, because the pinned scanner returns failures
// instead of folding them into an empty floor.
func allocateID(ctx context.Context, root, prefix string) (string, error) {
	floor, err := cardid.ScanFloor(filepath.Join(root, TasksDir), prefix)
	if err != nil {
		return "", err
	}
	if commonDir, ok := GitCommonDir(root); ok {
		refFloor, err := RefFloor(ctx, root, prefix)
		if err != nil {
			return "", err
		}
		if refFloor > floor {
			floor = refFloor
		}
		next, err := cardid.NewReservationLedger(filepath.Join(commonDir, "ce")).Next(prefix, floor)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s-%03d", prefix, next), nil
	}
	return fmt.Sprintf("%s-%03d", prefix, floor+1), nil
}

type cardFields struct{ Type, Priority, Effort, ExecTier, Agent string }

func resolveFields(kind string, opts NewCardOptions) (cardFields, error) {
	var types, priorities []string
	switch kind {
	case "plan":
		types, priorities = []string{"plan", "roadmap", "phase"}, nil
	case "issue":
		types, priorities = []string{"bug", "blocker", "tech-debt"}, []string{"P0", "P1", "P2", "P3"}
	case "backlog":
		types, priorities = []string{"idea", "feature", "improvement", "tech-debt"}, []string{"P1", "P2", "P3"}
	default:
		types, priorities = TaskTypes, PriorityValues
	}
	f := cardFields{
		Type:     strings.TrimSpace(opts.Type),
		Priority: strings.TrimSpace(opts.Priority),
		Effort:   strings.TrimSpace(opts.Effort),
		ExecTier: strings.TrimSpace(opts.ExecTier),
		Agent:    strings.TrimSpace(opts.Agent),
	}
	if f.Type == "" {
		f.Type = types[0]
	}
	if !contains(types, f.Type) {
		return f, fmt.Errorf("%w: --type %q is not valid for a %s card (valid: %s)", ErrNewCardInput, f.Type, kind, strings.Join(types, ", "))
	}
	if kind != "plan" {
		if f.Priority == "" {
			index := len(priorities) / 2
			if kind == "issue" {
				index = 1
			}
			f.Priority = priorities[index]
		}
		if !contains(priorities, f.Priority) {
			return f, fmt.Errorf("%w: --priority %q is not valid for a %s card (valid: %s)", ErrNewCardInput, f.Priority, kind, strings.Join(priorities, ", "))
		}
	}
	for _, check := range []struct {
		value string
		set   []string
		flag  string
	}{
		{f.Effort, []string{"XS", "S", "M", "L", "XL"}, "--effort"},
		{f.ExecTier, []string{"cheap", "standard", "strong"}, "--exec-tier"},
	} {
		if check.value != "" && !contains(check.set, check.value) {
			return f, fmt.Errorf("%w: %s %q (valid: %s)", ErrNewCardInput, check.flag, check.value, strings.Join(check.set, ", "))
		}
	}
	return f, nil
}

func contains(set []string, value string) bool {
	for _, s := range set {
		if s == value {
			return true
		}
	}
	return false
}

func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// render writes the canonical card template. Every field it emits is one the
// validator checks, so a freshly created card is a valid one.
func render(kind, id string, f cardFields, opts NewCardOptions) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", id)
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(strings.TrimSpace(opts.Title)))
	fmt.Fprintf(&b, "type: %s\n", f.Type)
	if f.Priority != "" {
		fmt.Fprintf(&b, "priority: %s\n", f.Priority)
	}
	if f.Effort != "" {
		fmt.Fprintf(&b, "effort: %s\n", f.Effort)
	}
	if f.ExecTier != "" {
		fmt.Fprintf(&b, "exec-tier: %s\n", f.ExecTier)
	}
	if f.Agent != "" {
		fmt.Fprintf(&b, "agent: %s\n", yamlQuote(f.Agent))
	}
	// The default card has no status line: the zone directory is the state,
	// and a fresh task enters the board at todo.
	switch kind {
	case "plan":
		fmt.Fprintf(&b, "scope: %s\nprogress: 0\ntotal-tasks: 0\ncompleted-tasks: 0\nchildren: []\ntarget-date: null\n", yamlQuote(strings.TrimSpace(opts.Title)))
	case "issue":
		if opts.Created == "" {
			opts.Created = time.Now().Format("2006-01-02")
		}
		fmt.Fprintf(&b, "severity: medium\ndiscovered-in: %s\ndiscovered-at: %s\n", yamlQuote(strings.TrimSpace(opts.Title)), opts.Created)
	}
	if opts.Created != "" {
		fmt.Fprintf(&b, "created: %s\n", opts.Created)
	}
	b.WriteString("---\n\n")

	switch kind {
	case "plan":
		b.WriteString("## Goal\n\n<!-- What done looks like for the whole plan. -->\n\n")
		b.WriteString("## Children\n\n")
		b.WriteString("<!-- Pre-conversion: fill the decomposition table. After a row becomes a card,\n")
		b.WriteString("     move it to frontmatter children and replace the row with `- TASK-NNN — title`. -->\n\n")
		b.WriteString("| # | 작업단위 | effort | 의존성 | exec-tier | 산출물 |\n")
		b.WriteString("|---|---------|--------|--------|-----------|--------|\n")
	case "issue":
		b.WriteString("## Summary\n\n<!-- One paragraph: what is wrong and who it affects. -->\n\n## Reproduction\n\n1. \n\n## Expected vs Actual\n\n- Expected: \n- Actual: \n")
	case "backlog":
		b.WriteString("## Description\n\n<!-- What the idea is. -->\n\n## Expected Value\n\n<!-- Why it would be worth doing. -->\n")
	default:
		b.WriteString("## Summary\n\n<!-- One paragraph: what changes and why. -->\n\n## Completion Criteria\n\n")
		if len(opts.Criteria) == 0 {
			b.WriteString("<!-- Each criterion needs a `| verify:` binding on its own line, or preflight refuses the card.\n")
			b.WriteString("     Command form:  ... | verify: `<command; exit 0 = pass>`\n")
			b.WriteString("     Human form:    ... | verify: human — <what a person checks> -->\n")
			b.WriteString("- [ ] <observable condition>\n")
		}
		for _, criterion := range opts.Criteria {
			fmt.Fprintf(&b, "- [ ] %s\n", strings.TrimSpace(criterion))
		}
	}
	return b.String()
}
