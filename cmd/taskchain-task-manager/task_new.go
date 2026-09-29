package main

// task_new.go: `taskchain-task-manager task new <kind>` creates a canonical
// card and takes its number from the cross-worktree reservation ledger in
// {git common dir}/ce. The id is taken last because taking one is a ledger
// write that remembers the number whether or not a card follows it — every
// input refusal above the allocation must stay above it, or a refused
// --priority burns a number.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
)

// runTask dispatches the `task` noun. The positional after `new` is the
// document kind — task, issue, plan, backlog — never the zone it lands in:
// zone is state, kind is type, and a card of kind task enters the board at
// todo/.
func runTask(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "new" {
		fmt.Fprintln(errOut, "usage: taskchain-task-manager task new <task|issue|plan|backlog> --title \"...\" [--criterion \"...\" ...]")
		return 2
	}
	return runTaskNew(args[1:], out, errOut)
}

const taskNewUsage = "usage: taskchain-task-manager task new <task|issue|plan|backlog> --title \"...\" [--criterion \"...\" ...]"

func runTaskNew(args []string, out, errOut io.Writer) int {
	var id, title, slug, cardType, priority, effort, execTier, agent, kind string
	var criteria []string
	jsonOut := false
	valued := map[string]*string{
		"--id":        &id,
		"--title":     &title,
		"--slug":      &slug,
		"--type":      &cardType,
		"--priority":  &priority,
		"--effort":    &effort,
		"--exec-tier": &execTier,
		"--agent":     &agent,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprintln(out, taskNewUsage)
			return 0
		case arg == "--json":
			jsonOut = true
		case arg == "--criterion":
			value, ok := flagValue(args, &i)
			if !ok {
				return taskNewInput(errOut, "--criterion needs a value")
			}
			criteria = append(criteria, value)
		case valued[arg] != nil:
			value, ok := flagValue(args, &i)
			if !ok {
				return taskNewInput(errOut, arg+" needs a value")
			}
			*valued[arg] = value
		case strings.HasPrefix(arg, "-"):
			return taskNewInput(errOut, fmt.Sprintf("unknown argument %s (valid: --id, --title, --slug, --type, --priority, --effort, --exec-tier, --agent, --criterion, --json, --help, -h)", arg))
		case kind != "":
			return taskNewInput(errOut, fmt.Sprintf("one kind only; got %q and %q", kind, arg))
		default:
			kind = arg
		}
	}
	if kind == "" {
		fmt.Fprintln(errOut, "Error: kind is required: task new <task|issue|plan|backlog> --title \"...\"")
		return 2
	}

	content, path, cardID, err := createCard(createCardOptions{
		Kind: kind, ID: id, Title: title, Slug: slug, Type: cardType,
		Priority: priority, Effort: effort, ExecTier: execTier, Agent: agent,
		Criteria: criteria, Created: time.Now().Format("2006-01-02"),
	})
	if err != nil {
		var inputErr *inputError
		if errors.As(err, &inputErr) {
			return taskNewInput(errOut, inputErr.msg)
		}
		fmt.Fprintf(errOut, "Error: %s\n", err)
		return 1
	}
	if jsonOut {
		if err := outputformat.Encode(out, map[string]string{"path": path, "id": cardID, "kind": kind}); err != nil {
			fmt.Fprintf(errOut, "Error: write result: %s\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(out, "✅ Created %s (%s)\n", path, cardID)
		if strings.Contains(content, "<observable condition>") {
			fmt.Fprintln(out, "   next: replace the placeholder criterion with real ones, each bound with `| verify:`")
		}
	}
	return 0
}

func taskNewInput(errOut io.Writer, msg string) int {
	fmt.Fprintf(errOut, "Error: new card: invalid input: %s\n", msg)
	return 2
}

func flagValue(args []string, i *int) (string, bool) {
	if *i+1 >= len(args) {
		return "", false
	}
	*i++
	return args[*i], true
}

// inputError marks a refusal caused by what the caller asked for, as opposed
// to a failure writing the file or reserving a number: the CLI maps the
// first to a usage exit and the others to runtime ones.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

func inputRefusal(format string, args ...any) error {
	return &inputError{msg: fmt.Sprintf(format, args...)}
}

type createCardOptions struct {
	Kind, ID, Title, Slug, Type, Priority, Effort, ExecTier, Agent string
	Criteria                                                       []string
	Created                                                        string
}

// kindTables hold each kind's vocabulary and where a fresh card lands. A
// work card has no kind directory and enters the board at its first
// workflow state; the other three park in directories named for the kind.
type kindTables struct {
	prefix, zone        string
	types               []string
	priorities          []string
	defaultPrioritySpot int
}

func kindTable(kind string) (kindTables, error) {
	switch kind {
	case "task":
		return kindTables{prefix: "TASK", zone: "todo",
			types:      []string{"feature", "bug", "chore", "refactor", "cleanup", "docs", "test"},
			priorities: []string{"P0", "P1", "P2", "P3"}, defaultPrioritySpot: 2}, nil
	case "issue":
		return kindTables{prefix: "ISSUE", zone: "issue",
			types:      []string{"bug", "blocker", "tech-debt"},
			priorities: []string{"P0", "P1", "P2", "P3"}, defaultPrioritySpot: 1}, nil
	case "plan":
		return kindTables{prefix: "PLAN", zone: "plan",
			types: []string{"plan", "roadmap", "phase"}}, nil
	case "backlog":
		return kindTables{prefix: "BACKLOG", zone: "backlog",
			types:      []string{"idea", "feature", "improvement", "tech-debt"},
			priorities: []string{"P1", "P2", "P3"}, defaultPrioritySpot: 1}, nil
	}
	if zoneWords[kind] {
		return kindTables{}, inputRefusal("%q is a zone name, not a kind (valid: task, issue, plan, backlog); a task card is created as `task` and lands in todo/", kind)
	}
	return kindTables{}, inputRefusal("unknown kind %q (valid: task, issue, plan, backlog)", kind)
}

// zoneWords is the closed set of workflow state names a caller might type
// where a kind belongs. Declared-zone vocabulary from a card dialect
// declaration is not consulted here; the dialect declaration is not part of
// this command's contract.
var zoneWords = map[string]bool{
	"todo": true, "todos": true, "pending": true, "active": true, "open": true,
	"ready": true, "doing": true, "wip": true, "in-progress": true,
	"review": true, "checked": true, "blocked": true, "cancelled": true,
	"done": true, "complete": true, "completed": true, "archived": true,
}

var (
	slugRe       = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	slugLetterRe = regexp.MustCompile(`[a-z]`)
)

func slugify(title string) string {
	return strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(title), "-"), "-")
}

// hasNonASCIITitleWord identifies title text slugify cannot represent: a
// non-ASCII letter or digit carries title meaning that an ASCII-only
// filename would silently lose.
func hasNonASCIITitleWord(title string) bool {
	for _, r := range title {
		if r > 0x7F {
			return true
		}
	}
	return false
}

// criterionBound reports whether the criterion carries a `| verify:` binding
// — a backtick command or `human — …` after the token. The full binding
// grammar, including inline code spans and truncation diagnosis, belongs to
// the validation-gate port; this is the creator's own input refusal.
func criterionBound(criterion string) bool {
	line := strings.TrimSpace(criterion)
	i := strings.Index(line, "| verify:")
	if i < 0 {
		return false
	}
	rest := strings.TrimLeft(line[i+len("| verify:"):], " \t")
	if strings.HasPrefix(rest, "`") {
		end := strings.Index(rest[1:], "`")
		return end > 0 && strings.TrimSpace(rest[1:1+end]) != ""
	}
	return strings.HasPrefix(rest, "human —") && strings.TrimSpace(rest[len("human —"):]) != ""
}

var cardFilenameRe = regexp.MustCompile(`^\d{2,3}-[a-z0-9]+(-[a-z0-9]+)*\.md$`)

// createCard runs the refusals in contract order, reserves the number
// last, and writes the card. It returns the rendered content, the
// board-relative path, and the id.
func createCard(opts createCardOptions) (content, path, cardID string, err error) {
	table, err := kindTable(opts.Kind)
	if err != nil {
		return "", "", "", err
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return "", "", "", inputRefusal("--title is required")
	}
	if explicitID := strings.TrimSpace(opts.ID); explicitID != "" {
		parsed, parseErr := cardid.Parse(explicitID)
		if parseErr != nil || parsed.Prefix != table.prefix {
			return "", "", "", inputRefusal("--id %q must look like %s-N", explicitID, table.prefix)
		}
	}
	slug := strings.TrimSpace(opts.Slug)
	if slug == "" {
		slug = slugify(title)
		if !slugLetterRe.MatchString(slug) {
			return "", "", "", inputRefusal("slug %q carries no ASCII letter -- digits left over from the title are not a slug; a title with no ASCII letters needs --slug", slug)
		}
	}
	if !slugRe.MatchString(slug) {
		return "", "", "", inputRefusal("slug %q is not lowercase-kebab; a title with no ASCII letters needs --slug", slug)
	}
	if strings.TrimSpace(opts.Slug) == "" && hasNonASCIITitleWord(title) {
		return "", "", "", inputRefusal("automatic slug %q would discard non-ASCII letters or digits from --title; pass --slug with a complete lowercase-kebab name", slug)
	}
	cardType := strings.TrimSpace(opts.Type)
	if cardType == "" {
		cardType = table.types[0]
	} else if !contains(table.types, cardType) {
		return "", "", "", inputRefusal("--type %q is not valid for a %s card (valid: %s)", cardType, opts.Kind, strings.Join(table.types, ", "))
	}
	priority := strings.TrimSpace(opts.Priority)
	if table.priorities != nil {
		if priority == "" {
			priority = table.priorities[table.defaultPrioritySpot]
		} else if !contains(table.priorities, priority) {
			return "", "", "", inputRefusal("--priority %q is not valid for a %s card (valid: %s)", priority, opts.Kind, strings.Join(table.priorities, ", "))
		}
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" && !contains([]string{"XS", "S", "M", "L", "XL"}, effort) {
		return "", "", "", inputRefusal("--effort %q (valid: XS, S, M, L, XL)", effort)
	}
	if tier := strings.TrimSpace(opts.ExecTier); tier != "" && !contains([]string{"cheap", "standard", "strong"}, tier) {
		return "", "", "", inputRefusal("--exec-tier %q (valid: cheap, standard, strong)", tier)
	}
	for _, criterion := range opts.Criteria {
		if !criterionBound(criterion) {
			return "", "", "", inputRefusal("criterion %q needs a backtick command or `human — …` after | verify", criterion)
		}
	}

	tasksDir, err := resolveTasksDir()
	if err != nil {
		return "", "", "", err
	}

	// The number is taken after every input refusal: reservation is a write.
	number := 0
	if explicitID := strings.TrimSpace(opts.ID); explicitID != "" {
		parsed, _ := cardid.Parse(explicitID)
		number = int(parsed.Number)
	} else {
		floor, scanErr := cardid.ScanFloor(tasksDir, table.prefix)
		if scanErr != nil {
			return "", "", "", fmt.Errorf("scan %s for the highest %s id: %w", tasksDir, table.prefix, scanErr)
		}
		if commonDir, ok := gitCommonDir(); ok {
			// The ledger and the tree scan both see only what has landed in
			// this worktree; the ledger exists so two worktrees creating a
			// card at once cannot compute the same answer. Without a
			// repository there is nothing to share, and the tree scan alone
			// numbers the board, as it always has.
			number, err = cardid.NewReservationLedger(filepath.Join(commonDir, "ce")).Next(table.prefix, floor)
			if err != nil {
				return "", "", "", err
			}
		} else {
			number = floor + 1
		}
	}
	if number > cardid.MaxCardNumber {
		return "", "", "", inputRefusal("filename %q does not match the card filename pattern", fmt.Sprintf("%03d-%s.md", number, slug))
	}
	cardID = fmt.Sprintf("%s-%03d", table.prefix, number)
	filename := fmt.Sprintf("%03d-%s.md", number, slug)
	if !cardFilenameRe.MatchString(filename) {
		return "", "", "", inputRefusal("filename %q does not match the card filename pattern", filename)
	}

	path = filepath.Join(tasksDir, table.zone, filename)
	content = renderCard(opts.Kind, cardID, title, cardType, priority, opts)
	if err := writeNewFile(path, content); err != nil {
		return "", "", "", err
	}
	return content, path, cardID, nil
}

// resolveTasksDir reads TASKS_DIR from the environment, defaulting to
// "tasks". An absolute value is refused: the name is relative to the working
// directory, and a joined absolute path silently becomes a directory nothing
// populated.
func resolveTasksDir() (string, error) {
	tasksDir := os.Getenv("TASKS_DIR")
	if tasksDir == "" {
		return "tasks", nil
	}
	if filepath.IsAbs(tasksDir) {
		return "", inputRefusal("TASKS_DIR must be a directory name relative to the working directory, not an absolute path: %s", tasksDir)
	}
	return tasksDir, nil
}

// gitCommonDir resolves the repository's shared git directory, where the
// reservation ledger lives so every worktree sees one ledger. It reports
// false whenever git declines — no repository, git unavailable — which is
// the caller's signal to fall back to its own tree scan.
func gitCommonDir() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	outBytes, err := exec.CommandContext(ctx, "git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", false
	}
	path := strings.TrimSpace(string(outBytes))
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path, err = filepath.Abs(path)
		if err != nil {
			return "", false
		}
	}
	return path, true
}

// writeNewFile creates the card's parent directories and the file, refusing
// an existing path: creation never overwrites.
func writeNewFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create card directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("card already exists: %s", path)
		}
		return fmt.Errorf("write card: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("write card: %w", err)
	}
	return f.Sync()
}

func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// renderCard writes a card every field of which a validator checks, so a
// freshly created card is a valid one. Field order and the per-kind bodies
// are the observable contract of creation.
func renderCard(kind, id, title, cardType, priority string, opts createCardOptions) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", id)
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(title))
	fmt.Fprintf(&b, "type: %s\n", cardType)
	if priority != "" {
		fmt.Fprintf(&b, "priority: %s\n", priority)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		fmt.Fprintf(&b, "effort: %s\n", effort)
	}
	if tier := strings.TrimSpace(opts.ExecTier); tier != "" {
		fmt.Fprintf(&b, "exec-tier: %s\n", tier)
	}
	if agent := strings.TrimSpace(opts.Agent); agent != "" {
		fmt.Fprintf(&b, "agent: %s\n", yamlQuote(agent))
	}
	switch kind {
	case "plan":
		fmt.Fprintf(&b, "scope: %s\nprogress: 0\ntotal-tasks: 0\ncompleted-tasks: 0\nchildren: []\ntarget-date: null\n", yamlQuote(title))
	case "issue":
		fmt.Fprintf(&b, "severity: medium\ndiscovered-in: %s\ndiscovered-at: %s\n", yamlQuote(title), opts.Created)
	}
	if opts.Created != "" {
		fmt.Fprintf(&b, "created: %s\n", opts.Created)
	}
	b.WriteString("---\n\n")

	switch kind {
	case "plan":
		b.WriteString("## Goal\n\n<!-- What done looks like for the whole plan. -->\n\n## Children\n\n<!-- Pre-conversion: fill the decomposition table. After a row becomes a card,\n     move it to frontmatter children and replace the row with `- TASK-NNN — title`. -->\n\n| # | 작업단위 | effort | 의존성 | exec-tier | 산출물 |\n|---|---------|--------|--------|-----------|--------|\n")
	case "issue":
		b.WriteString("## Summary\n\n<!-- One paragraph: what is wrong and who it affects. -->\n\n## Reproduction\n\n1. \n\n## Expected vs Actual\n\n- Expected: \n- Actual: \n")
	case "backlog":
		b.WriteString("## Description\n\n<!-- What the idea is. -->\n\n## Expected Value\n\n<!-- Why it would be worth doing. -->\n")
	default:
		fmt.Fprintf(&b, "## Summary\n\n<!-- One paragraph: what changes and why. -->\n\n## Completion Criteria\n\n")
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

func contains(set []string, value string) bool {
	for _, s := range set {
		if s == value {
			return true
		}
	}
	return false
}
