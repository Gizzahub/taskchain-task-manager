package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// The readiness commands: lint judges the filing, preflight judges the queue,
// and gate runs both plus validate plus checked-binding re-execution and turns
// the whole board into one verdict. Each mirrors the pinned reference's exit
// discipline — the verdict is exit 1, "called wrong" or "could not judge" is
// exit 2 — so a caller branches on one code and is never lied to about which
// of those happened.

func taskLint(ctx context.Context, args []string, out io.Writer) error {
	asJSON := false
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprintln(out, "usage: taskchain-task-manager task lint [--json]")
			return nil
		case arg == "--json":
			asJSON = true
		default:
			return usagef("unknown argument %s (valid: --json, --help, -h)", arg)
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	report := taskflow.Lint(root)
	if asJSON {
		if err := taskflow.RenderLintJSON(out, report); err != nil {
			return err
		}
	} else {
		taskflow.RenderLint(out, report)
	}
	if !report.Clean {
		return fmt.Errorf("%s", taskflow.LintFailureDetail(report))
	}
	return nil
}

func taskPreflight(ctx context.Context, args []string, out io.Writer) error {
	opts := taskflow.PreflightOptions{}
	asJSON := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprintln(out, "usage: taskchain-task-manager task preflight [--zone <zone>[,<zone>]] [--max-card-bytes N] [--json]")
			return nil
		case arg == "--json":
			asJSON = true
		case arg == "--zone":
			value, err := flagValue(args, i)
			if err != nil {
				return usagef("%s", err)
			}
			i++
			opts.Zones = append(opts.Zones, splitPreflightZones(value)...)
		case len(arg) > 7 && arg[:7] == "--zone=":
			opts.Zones = append(opts.Zones, splitPreflightZones(arg[7:])...)
		case arg == "--max-card-bytes":
			value, err := flagValue(args, i)
			if err != nil {
				return usagef("%s", err)
			}
			i++
			n, err := parsePreflightInt(value)
			if err != nil {
				return usagef("--max-card-bytes: %v", err)
			}
			opts.MaxCardBytes = n
		case len(arg) > 17 && arg[:17] == "--max-card-bytes=":
			n, err := parsePreflightInt(arg[17:])
			if err != nil {
				return usagef("--max-card-bytes: %v", err)
			}
			opts.MaxCardBytes = n
		default:
			return usagef("unknown flag: %s (valid: --zone, --max-card-bytes, --json, --help, -h)", arg)
		}
	}
	if err := taskflow.ValidatePreflightZones(opts.Zones); err != nil {
		return usagef("%s", err)
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	report := taskflow.PreflightCheck(root, opts)
	if asJSON {
		if err := taskflow.RenderPreflightJSON(out, report); err != nil {
			return err
		}
	} else {
		taskflow.RenderPreflightReport(out, report, opts.MaxCardBytes, false)
	}
	if err := taskflow.PreflightExitError(report); err != nil {
		return err
	}
	return nil
}

func taskGate(ctx context.Context, args []string, out io.Writer) error {
	asJSON := false
	dir := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			printTaskGateHelp(out)
			return nil
		case arg == "--json":
			asJSON = true
		case arg == "--dir":
			value, err := flagValue(args, i)
			if err != nil {
				return usagef("%s", err)
			}
			i++
			dir = value
		case len(arg) > 6 && arg[:6] == "--dir=":
			dir = arg[6:]
		default:
			return usagef("unknown flag: %s (valid: --dir, --json, --help, -h)", arg)
		}
	}
	if dir != "" {
		restore, err := enterGateDir(dir)
		if err != nil {
			return usagef("%s", err)
		}
		defer restore()
	}
	// The refusal lands before any stdout: a gate that prints a report about
	// a tree it could not find has already lied about having measured
	// something.
	if err := taskflow.RequireTasksDirectory(); err != nil {
		return usagef("%s", err)
	}
	report := taskflow.RunGate(ctx, out, asJSON)
	if asJSON {
		if err := taskflow.RenderGateJSON(out, report); err != nil {
			return err
		}
	} else {
		taskflow.RenderGateReport(out, report)
	}
	switch report.Status {
	case "ready":
		return nil
	case "unavailable":
		return usagef("%s", report.Summary)
	default:
		return fmt.Errorf("%s", report.Summary)
	}
}

// enterGateDir moves into the repository the caller named and hands back the
// way home. The gate's checks all read the working directory, the way every
// other task command does, so --dir is honoured here rather than threaded
// through each check.
func enterGateDir(dir string) (func(), error) {
	previous, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("--dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--dir %s is not a directory", dir)
	}
	if err := os.Chdir(dir); err != nil {
		return nil, fmt.Errorf("--dir %s: %w", dir, err)
	}
	return func() { _ = os.Chdir(previous) }, nil
}

func printTaskGateHelp(out io.Writer) {
	fmt.Fprintln(out, `taskchain-task-manager task gate — one verdict for the whole task board

USAGE:
  taskchain-task-manager task gate [--dir PATH] [--json]

Runs, in order and stopping at the first failure:
  validate    task validate --all
  lint        task lint
  preflight   shared preflight checks (NO_QUEUE is valid for a cleared board)
  bindings    re-execute every checked [x] machine binding in the repository
              root (30s each; human — and unchecked bindings are not run) and
              refuse a board no card was read from

EXIT CODES:
  0  ready        every check passed and at least one card was examined
  1  not-ready    a check failed; the board is not integrable as written
  2  unavailable  the gate could not measure (bad flag, no tasks directory,
                  or a tasks tree with no cards to examine)

OPTIONS:
  --dir PATH  repository root to judge (default: working directory)
  --json      one object on stdout: status, summary, failed_step, steps[]`)
}

func splitPreflightZones(value string) []string {
	var zones []string
	for _, z := range strings.Split(value, ",") {
		if z = strings.TrimSpace(z); z != "" {
			zones = append(zones, z)
		}
	}
	return zones
}

func parsePreflightInt(value string) (int, error) {
	return strconv.Atoi(value)
}
