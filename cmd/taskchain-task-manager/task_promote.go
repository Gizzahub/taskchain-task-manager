package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

func taskPromote(ctx context.Context, args []string, out io.Writer) error {
	positional, asJSON, dryRun, err := parsePromoteArgs(args, out)
	if errors.Is(err, errPrintedHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		return fmt.Errorf("usage: ce task promote <issue-id|path>... | ce task promote issues")
	}
	allOpen := len(positional) == 1 && positional[0] == "issues"
	if !allOpen {
		for _, arg := range positional {
			if arg == "issues" {
				return fmt.Errorf("`issues` selects every open issue and cannot be combined with named targets")
			}
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	opts := taskflow.PromoteOptions{
		AllOpen: allOpen,
		DryRun:  dryRun,
		Created: time.Now().Format("2006-01-02"),
	}
	if !allOpen {
		opts.Targets = positional
	}
	result, err := taskflow.Promote(ctx, root, opts)
	if err != nil {
		if errors.Is(err, taskflow.ErrPromoteInput) || errors.Is(err, taskflow.ErrNewCardInput) {
			return usagef("%s", err.Error())
		}
		return err
	}
	if asJSON {
		return taskflow.RenderPromoteJSON(out, result)
	}
	taskflow.RenderPromoteText(out, result)
	return nil
}

func parsePromoteArgs(args []string, out io.Writer) ([]string, bool, bool, error) {
	var positional []string
	asJSON, dryRun := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			fmt.Fprint(out, promoteHelp)
			return nil, false, false, errPrintedHelp
		}
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		switch arg {
		case "--json":
			asJSON = true
		case "--dry-run":
			dryRun = true
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, false, false, fmt.Errorf("unknown flag: %s (valid: --dry-run, --json, --help, -h)", arg)
			}
			positional = append(positional, arg)
		}
	}
	return positional, asJSON, dryRun, nil
}

func taskIssuePromote(ctx context.Context, args []string, out io.Writer) error {
	selected := 0
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			selected++
		}
	}
	if selected > 1 {
		return fmt.Errorf("ce task issue-promote accepts exactly one issue; use ce task issue-promote-all for all open issues")
	}
	asJSON, dryRun, targets, err := parseStateArgs("issue-promote", args, out)
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
	result, err := taskflow.SelectState(root, "issue-promote", targets)
	if err != nil {
		return err
	}
	if len(targets) > 0 && !dryRun {
		if err := guardIssuePromotionApply(root); err != nil {
			return err
		}
		promoteArgs := make([]string, 0, len(result.Candidates)+1)
		for _, card := range result.Candidates {
			promoteArgs = append(promoteArgs, card.Path)
		}
		if asJSON {
			promoteArgs = append(promoteArgs, "--json")
		}
		return taskPromote(ctx, promoteArgs, out)
	}
	return writeState(out, result, asJSON)
}

func guardIssuePromotionApply(root string) error {
	branch := exec.Command("git", "--no-replace-objects", "--no-lazy-fetch", "branch", "--show-current")
	branch.Dir = root
	current, err := branch.Output()
	if err != nil {
		return fmt.Errorf("issue-promote: read current branch: %w", err)
	}
	if strings.TrimSpace(string(current)) == "" {
		return fmt.Errorf("issue-promote refuses detached HEAD")
	}
	return nil
}

const promoteHelp = `Usage: taskchain-task-manager task promote <issue-id|path>... [--dry-run] [--json]
       taskchain-task-manager task promote issues [--dry-run] [--json]

Turn an open issue into one TASK card. issues sweeps every open issue.
Skipped issues are reported and are not errors. --dry-run writes nothing
and allocates no id.

Options:
  --dry-run   Report candidates and skips without writing
  --json      Machine-readable output
  --help, -h  Show this help

Anything else flag-shaped is refused by name. -- ends the flags.
`
