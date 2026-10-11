package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// taskUsage is the noun's one-line contract, shown when the noun itself is
// mistyped. Per-subcommand misuse is reported by the subcommand, at the exit
// code the pinned reference gives that subcommand's own refusals.
const taskUsage = "usage: taskchain-task-manager task <list|new|move|archive|validate|lint|preflight|gate|review-receipt|doctor|board|recommend|promote|issue-promote|plan-promote|backlog-promote|todo-run|doing-resume|review-run|blocked-recover|done-finalize|done-finalize-all|cleanup|maintenance|reconcile|run-doctor|run-start|run-status|run-list|run-finish|run-recover|run-abort|run-close|run-settle|run-discard> ..."

// taskCommandList is the unknown-command diagnostic. It names every command
// this noun dispatches, in the same order as taskUsage.
const taskCommandList = "list, new, move, archive, validate, lint, preflight, gate, review-receipt, doctor, board, recommend, promote, issue-promote, plan-promote, backlog-promote, todo-run, doing-resume, review-run, blocked-recover, done-finalize, done-finalize-all, cleanup, maintenance, reconcile, run-doctor, run-start, run-status, run-list, run-finish, run-recover, run-abort, run-close, run-settle, run-discard"

// runTask dispatches the CE-parity `task` noun. The pinned reference splits
// its refusals two ways, and the split is preserved here: what the caller
// typed wrong for `new` and `validate` is a usage error (exit 2), while
// `list`, `move`, and `archive` report their arg trouble as ordinary errors
// (exit 1). Operational failures always print `Error: ...` on stderr.
func runTask(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, taskUsage)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(out, taskUsage)
		return 0
	}
	// TASKS_DIR redirects the whole noun at another corpus; an absolute value
	// is refused once here so no subcommand can silently read an empty board.
	// validate --staged reads the Git index and ignores TASKS_DIR.
	if !validateStagedIgnoresTasksDir(args) {
		if _, err := taskflow.ResolveTasksDir(); err != nil {
			fmt.Fprintln(errOut, "Error: "+err.Error())
			return 2
		}
	}
	ctx := context.Background()
	var err error
	switch args[0] {
	case "list":
		err = taskList(args[1:], out)
	case "new":
		err = taskNew(ctx, args[1:], out)
	case "move":
		err = taskMove(ctx, args[1:], out)
	case "archive":
		err = taskArchive(args[1:], out)
	case "validate":
		err = taskValidate(ctx, args[1:], out, errOut)
	case "lint":
		err = taskLint(ctx, args[1:], out)
	case "preflight":
		err = taskPreflight(ctx, args[1:], out)
	case "gate":
		err = taskGate(ctx, args[1:], out)
	case "board":
		err = taskBoard(ctx, args[1:], out)
	case "recommend":
		err = taskRecommend(ctx, args[1:], out)
	case "promote":
		err = taskPromote(ctx, args[1:], out)
	case "issue-promote":
		err = taskIssuePromote(ctx, args[1:], out)
	case "plan-promote":
		err = taskStateCommand(ctx, "plan-promote", args[1:], out)
	case "backlog-promote":
		err = taskStateCommand(ctx, "backlog-promote", args[1:], out)
	case "todo-run":
		err = taskStateCommand(ctx, "todo-run", args[1:], out)
	case "doing-resume":
		err = taskStateCommand(ctx, "doing-resume", args[1:], out)
	case "review-run":
		err = taskStateCommand(ctx, "review-run", args[1:], out)
	case "blocked-recover":
		err = taskStateCommand(ctx, "blocked-recover", args[1:], out)
	case "done-finalize":
		err = taskStateCommand(ctx, "done-finalize", args[1:], out)
	case "done-finalize-all":
		err = taskDoneFinalizeAll(ctx, args[1:], out)
	case "cleanup":
		err = taskCleanup(ctx, args[1:], out)
	case "maintenance":
		err = taskMaintenance(ctx, args[1:], out)
	case "reconcile":
		err = taskReconcile(ctx, args[1:], out)
	case "done-review":
		// Removed. The replacement is review-run; this is not a dispatch alias.
		err = fmt.Errorf("ce task done-review was removed; use ce task review-run")
	case "run-doctor":
		err = taskRunDoctor(args[1:], out)
	case "run-start":
		err = taskRunStart(args[1:], out)
	case "run-status":
		err = taskRunStatus(args[1:], out)
	case "run-list":
		err = taskRunList(args[1:], out)
	case "review-receipt":
		err = taskReviewReceipt(args[1:], out)
	case "doctor":
		err = taskDoctor(ctx, args[1:], out)
	case "run-finish":
		err = taskRunFinish(args[1:], out)
	case "run-recover":
		err = taskRunRecover(args[1:], out)
	case "run-abort":
		err = taskRunAbort(args[1:], out)
	case "run-close":
		err = taskRunClose(args[1:], out)
	case "run-settle":
		err = taskRunSettle(args[1:], out)
	case "run-discard":
		err = taskRunDiscard(args[1:], out)
	default:
		fmt.Fprintf(errOut, "Unknown task command: %s\nAvailable: %s\n", args[0], taskCommandList)
		return 1
	}
	if err == nil {
		return 0
	}
	var usage *taskUsageError
	if errors.As(err, &usage) {
		fmt.Fprintf(errOut, "Error: %v\n", usage.err)
		return 2
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		fmt.Fprintf(errOut, "Error: %v\n", err)
		return coded.ExitCode()
	}
	fmt.Fprintf(errOut, "Error: %v\n", err)
	return 1
}

// taskUsageError marks a refusal caused by what the caller typed, for `new`
// and `validate`, the two subcommands the reference exits 2 on.
type taskUsageError struct{ err error }

func (e *taskUsageError) Error() string { return e.err.Error() }

func usagef(format string, args ...any) *taskUsageError {
	return &taskUsageError{err: fmt.Errorf(format, args...)}
}

// validateStagedIgnoresTasksDir reports whether this invocation is
// `task validate --staged`, the one mode that does not read TASKS_DIR.
func validateStagedIgnoresTasksDir(args []string) bool {
	if len(args) == 0 || args[0] != "validate" {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "--staged" {
			return true
		}
	}
	return false
}

func taskNew(ctx context.Context, args []string, out io.Writer) error {
	opts, asJSON, err := parseTaskNewArgs(args, out)
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	opts.Root = root
	opts.Created = time.Now().Format("2006-01-02")

	result, err := taskflow.Create(ctx, opts)
	if err != nil {
		if errors.Is(err, taskflow.ErrNewCardInput) {
			return usagef("%s", err)
		}
		return err
	}
	// Validate what was written rather than trusting the writer: the creator
	// and the validator are two readings of one contract.
	verdict, err := validateWrittenCard(ctx, root, result.Path)
	if err != nil {
		return fmt.Errorf("validate %s after writing it: %w", result.Path, err)
	}
	if asJSON {
		if err := printNewCardJSON(out, result, verdict); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "✅ Created %s (%s)\n", result.Path, result.ID)
		for _, e := range verdict.Errors {
			fmt.Fprintf(out, "   ❌ %s: %s\n", e.Field, e.Message)
		}
		for _, w := range verdict.Warnings {
			fmt.Fprintf(out, "   ⚠️  %s: %s\n", w.Field, w.Message)
		}
		if strings.Contains(result.Content, "<observable condition>") {
			fmt.Fprintln(out, "   next: replace the placeholder criterion with real ones, each bound with `| verify:`")
		}
	}
	if !verdict.Valid() {
		return fmt.Errorf("%s was written but does not validate", result.Path)
	}
	return nil
}

func parseTaskNewArgs(args []string, out io.Writer) (taskflow.NewCardOptions, bool, error) {
	var opts taskflow.NewCardOptions
	asJSON := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprintln(out, taskUsage)
			return opts, false, nil
		case arg == "--json":
			asJSON = true
		case arg == "--criterion":
			value, err := flagValue(args, i)
			if err != nil {
				return opts, false, usagef("%s", err)
			}
			i++
			opts.Criteria = append(opts.Criteria, value)
		default:
			known, err := setTaskNewFlag(&opts, arg, args, i)
			if err != nil {
				return opts, false, usagef("%s", err)
			}
			if known {
				i++
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return opts, false, usagef("unknown argument %s (valid: --id, --title, --slug, --type, --priority, --effort, --exec-tier, --agent, --discovered-in, --criterion, --json, --help, -h)", arg)
			}
			if opts.Kind != "" {
				return opts, false, usagef("one kind only; got %q and %q", opts.Kind, arg)
			}
			opts.Kind = arg
		}
	}
	if opts.Kind == "" {
		return opts, false, usagef("kind is required: task new <task|issue|plan|backlog> --title \"...\"")
	}
	return opts, asJSON, nil
}

// setTaskNewFlag applies one valued flag, reporting whether the argument was
// one of the noun's flags at all. It consumes the value by incrementing the
// caller's index responsibility: the flag sits at i, its value at i+1.
func setTaskNewFlag(opts *taskflow.NewCardOptions, arg string, args []string, i int) (bool, error) {
	valued := map[string]*string{
		"--id":            &opts.ID,
		"--title":         &opts.Title,
		"--slug":          &opts.Slug,
		"--type":          &opts.Type,
		"--priority":      &opts.Priority,
		"--effort":        &opts.Effort,
		"--exec-tier":     &opts.ExecTier,
		"--agent":         &opts.Agent,
		"--discovered-in": &opts.DiscoveredIn,
	}
	target, ok := valued[arg]
	if !ok {
		return false, nil
	}
	v, err := flagValue(args, i)
	if err != nil {
		return true, err
	}
	*target = v
	if arg == "--discovered-in" {
		opts.DiscoveredInSet = true
	}
	return true, nil
}

func flagValue(args []string, i int) (string, error) {
	if i+1 >= len(args) {
		return "", fmt.Errorf("%s needs a value", args[i])
	}
	return args[i+1], nil
}

// printNewCardJSON is the machine-readable side of `new`. It is the same
// verdict the prose prints, so a script gating on either sees one truth.
func printNewCardJSON(out io.Writer, result *taskflow.NewCardResult, verdict taskflow.ValidationResult) error {
	payload := struct {
		Path     string   `json:"path"`
		ID       string   `json:"id"`
		Kind     string   `json:"kind"`
		Valid    bool     `json:"valid"`
		Errors   []string `json:"errors"`
		Warnings []string `json:"warnings"`
	}{
		Path: result.Path, ID: result.ID, Kind: result.Kind,
		Valid: verdict.Valid(), Errors: []string{}, Warnings: []string{},
	}
	for _, e := range verdict.Errors {
		payload.Errors = append(payload.Errors, e.Field+": "+e.Message)
	}
	for _, w := range verdict.Warnings {
		payload.Warnings = append(payload.Warnings, w.Field+": "+w.Message)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(data))
	return nil
}

// validateWrittenCard re-reads and validates exactly the card that was written.
func validateWrittenCard(ctx context.Context, root, cardPath string) (taskflow.ValidationResult, error) {
	results, _, err := taskflow.ValidateAll(ctx, root)
	if err != nil {
		return taskflow.ValidationResult{}, err
	}
	for _, result := range results {
		if result.Path == cardPath {
			return result, nil
		}
	}
	return taskflow.ValidationResult{
		Path:   cardPath,
		Errors: []taskflow.Finding{{Field: "board", Message: "written card not found on re-read"}},
	}, nil
}

func taskMove(ctx context.Context, args []string, out io.Writer) error {
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			fmt.Fprintln(out, taskUsage)
			return nil
		}
		if arg == "--" {
			// End of flags: what follows is positional even when it reads like
			// a flag, so `move -- --help doing` moves a card named --help.
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("unknown flag: %s (valid: --help, -h)", arg)
		}
		positional = append(positional, arg)
	}
	if len(positional) < 2 {
		return errors.New("usage: task move <task-file> <zone|status>\n" +
			"  zone/status: todo|doing|review|blocked|done (pending/in-progress/... also accepted)")
	}
	if len(positional) > 2 {
		return fmt.Errorf("unexpected argument: %s (task move takes two arguments)", positional[2])
	}
	word := positional[1]
	dest, ok := taskflow.ResolveMoveDestination(word)
	if !ok {
		// A status the vocabulary knows but cannot park a card in is answered
		// on its own terms: `cancelled` is a real status whose destination is
		// storage, not a sixth zone.
		if st, isStatus := taskflow.StatusFromWord(word); isStatus && st.Dir() == "" {
			return fmt.Errorf("%q is a status, not a zone: a %s card goes to storage rather than to a "+
				"workflow directory, so `task archive` is the command for it", word, st)
		}
		return fmt.Errorf("unknown target zone/status %q (this repository accepts: todo, doing, review, blocked, done)", word)
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	newRel, synced, links, err := taskflow.MoveCard(root, positional[0], dest)
	if err != nil {
		return fmt.Errorf("move failed: %s", err)
	}
	fmt.Fprintf(out, "✅ Moved to %s: %s\n", dest.Zone, filepath.Join(taskflow.TasksDirName(), newRel))
	switch {
	case synced:
		fmt.Fprintf(out, "   Status cell synced → %s\n", dest.Status.StatusCell())
	case dest.Terminal != "":
		fmt.Fprintf(out, "   Recorded as %s in the card's frontmatter\n", dest.Terminal)
	case dest.Status == "":
		fmt.Fprintf(out, "   (zone %q declares no zone-status:, so the **Status** cell was left as it was)\n", dest.Zone)
	default:
		fmt.Fprintln(out, "   (no **Status** cell found to sync)")
	}
	printTaskLinkUpdate(out, "   ", links)
	return nil
}

// cardTasksRel normalizes the caller's path to its tasks-relative form so the
// reader and the mover see the same file.
func cardTasksRel(root, taskFile string) string {
	cleaned := filepath.Clean(taskFile)
	if rel, err := filepath.Rel(filepath.Join(root, taskflow.TasksDirName()), filepath.Join(root, cleaned)); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(cleaned)
}

// printTaskLinkUpdate renders what a relocation did to the relative references
// around the card. Every line is an edit to a file the caller did not name, so
// a command that edits a file must say so.
func printTaskLinkUpdate(out io.Writer, prefix string, update taskflow.LinkUpdate) {
	if update.Skipped != "" {
		fmt.Fprintf(out, "%s⚠️  relative references not reconciled: %s\n", prefix, update.Skipped)
		return
	}
	if n := len(update.Outbound); n > 0 {
		fmt.Fprintf(out, "%s%d relative link(s) in the card retargeted for its new depth:\n", prefix, n)
		for _, rewrite := range update.Outbound {
			fmt.Fprintf(out, "%s  line %d: %s → %s\n", prefix, rewrite.Line, rewrite.From, rewrite.To)
		}
	}
	if len(update.Inbound) == 0 {
		return
	}
	fmt.Fprintf(out, "%s%d document(s) pointing at the card were retargeted:\n", prefix, len(update.Inbound))
	for _, inbound := range update.Inbound {
		for _, line := range inbound.Lines {
			fmt.Fprintf(out, "%s  %s:%d\n", prefix, inbound.Path, line)
		}
	}
}
