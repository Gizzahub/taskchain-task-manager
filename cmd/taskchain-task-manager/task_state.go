package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// errPrintedHelp tells the caller that --help already wrote stdout.
var errPrintedHelp = errors.New("help printed")

func taskStateCommand(ctx context.Context, action string, args []string, out io.Writer) error {
	_ = ctx
	asJSON, _, targets, err := parseStateArgs(action, args, out)
	if errors.Is(err, errPrintedHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	result, err := taskflow.SelectState(root, action, targets)
	if err != nil {
		return err
	}
	// cleanup is not in this build. A named card would delete; refuse instead.
	if action == "done-finalize" && len(targets) > 0 {
		return fmt.Errorf("done-finalize refuses to write: cleanup is not available, so a named card is not deleted")
	}
	return writeState(out, result, asJSON)
}

func parseStateArgs(action string, args []string, out io.Writer) (bool, bool, []string, error) {
	var targets []string
	asJSON, dryRun := false, false
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprint(out, stateHelp(action))
			return false, false, nil, errPrintedHelp
		case arg == "--json":
			asJSON = true
		case arg == "--dry-run":
			dryRun = true
		case strings.HasPrefix(arg, "-"):
			return false, false, nil, fmt.Errorf("unknown flag: %s (valid: --dry-run, --json, --help, -h)", arg)
		default:
			targets = append(targets, arg)
		}
	}
	return asJSON, dryRun, targets, nil
}

func writeState(out io.Writer, result *taskflow.StateResult, asJSON bool) error {
	if asJSON {
		if result.Candidates == nil {
			result.Candidates = []taskflow.StateCandidate{}
		}
		return outputformat.Encode(out, result)
	}
	taskflow.RenderStateText(out, result)
	return nil
}

func stateHelp(action string) string {
	zone := map[string]string{
		"todo-run": "todo", "doing-resume": "doing", "review-run": "review",
		"done-finalize": "done", "blocked-recover": "blocked",
		"issue-promote": "issue", "plan-promote": "plan", "backlog-promote": "backlog",
	}[action]
	argument := "[card...]"
	if action == "issue-promote" {
		argument = "[ISSUE]"
	}
	body := "Read-only agent handoff: select and validate state; execution/review requires the referenced Skill."
	if action == "done-finalize" || action == "issue-promote" {
		body = "Explicit cards apply by default; --dry-run only prepares candidates. Without cards, returns selection context."
	}
	return fmt.Sprintf("Usage: taskchain-task-manager task %s %s [--dry-run] [--json]\n\nOnly %s/ cards are eligible. No cross-state fallback.\n%s\n\nOptions:\n  --dry-run   Prepare the selection without applying it\n  --json      Machine-readable output\n  --help, -h  Show this help\n", action, argument, zone, body)
}
