package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

const listUsage = "usage: taskchain-task-manager task list [--status pending|in-progress|review|blocked|done|cancelled] [--priority P0|P1|P2|P3] [--category CATEGORY] [--kind task|issue|plan|backlog] [--state open|blocked|promoted|resolved] [--summary] [--json]"

func taskList(args []string, out io.Writer) error {
	opts, filter, err := parseTaskListArgs(args, out)
	if err != nil || opts.help {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if !filter.Active() && !opts.summary {
		if opts.asJSON {
			return taskflow.ListJSON(out, root)
		}
		return taskflow.ListText(out, root)
	}
	cards, err := taskflow.FilterCards(root, filter)
	if err != nil {
		return err
	}
	if opts.summary {
		return taskflow.RenderListSummary(out, cards, opts.asJSON)
	}
	if opts.asJSON {
		return taskflow.RenderFilteredJSON(out, root, cards)
	}
	taskflow.RenderFilteredList(out, root, cards, filter)
	return nil
}

type listOpts struct {
	asJSON  bool
	summary bool
	help    bool
}

func parseTaskListArgs(args []string, out io.Writer) (listOpts, taskflow.ListFilter, error) {
	var opts listOpts
	var filter taskflow.ListFilter
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			fmt.Fprintln(out, listUsage)
			return listOpts{help: true}, filter, nil
		case "--json":
			opts.asJSON = true
		case "--summary":
			opts.summary = true
		case "--status", "--priority", "--category", "--kind", "--state":
			value, err := flagValue(args, i)
			if err != nil {
				return opts, filter, err
			}
			i++
			switch arg {
			case "--status":
				if !oneOf(value, "pending", "in-progress", "review", "blocked", "done", "cancelled") {
					return opts, filter, fmt.Errorf("unknown --status value: %s (valid: pending, in-progress, review, blocked, done, cancelled)", value)
				}
				filter.Status = value
			case "--priority":
				if !oneOf(value, "P0", "P1", "P2", "P3") {
					return opts, filter, fmt.Errorf("unknown --priority value: %s (valid: P0, P1, P2, P3)", value)
				}
				filter.Priority = value
			case "--category":
				filter.Category = value
			case "--kind":
				if !oneOf(value, "task", "issue", "plan", "backlog") {
					return opts, filter, fmt.Errorf("unknown --kind value: %s (valid: task, issue, plan, backlog)", value)
				}
				filter.Kind = value
			case "--state":
				if !oneOf(value, "open", "blocked", "promoted", "resolved") {
					return opts, filter, fmt.Errorf("unknown --state value: %s (valid: open, blocked, promoted, resolved)", value)
				}
				filter.State = value
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, filter, fmt.Errorf("unknown flag: %s (valid: --status, --priority, --category, --kind, --state, --summary, --json, --help, -h)", arg)
			}
			return opts, filter, usagef("unexpected argument: %s", arg)
		}
	}
	return opts, filter, nil
}

func oneOf(value string, valid ...string) bool {
	for _, item := range valid {
		if value == item {
			return true
		}
	}
	return false
}
