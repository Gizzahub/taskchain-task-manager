package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// The validation-gate surface of the task noun: lint, preflight, and the gate
// that re-executes what checked bindings claim. Each reports its verdict as
// the requested data on stdout and keeps stderr to the refusal line.

// taskLint surveys the board's placement. The verdict text is the data the
// caller asked for; the error carries only the census line, so stderr stays
// diagnostics and stdout stays the report.
func taskLint(args []string, out io.Writer) error {
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprintln(out, taskUsage)
			return nil
		default:
			return fmt.Errorf("unknown argument: %s (this port takes no arguments)", arg)
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	census, err := taskflow.CollectLint(root)
	if err != nil {
		return err
	}
	taskflow.RenderLintText(out, census)
	if !census.Clean() {
		return fmt.Errorf("%s", census.CensusLine())
	}
	return nil
}

// taskPreflight reads the board's metadata and reports whether the queue can
// run. A NOT_READY verdict is data, not an error: the text already says it, so
// the refusal line is the only stderr.
func taskPreflight(args []string, out io.Writer) error {
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		case "--help", "-h":
			fmt.Fprintln(out, taskUsage)
			return nil
		default:
			return fmt.Errorf("unknown argument: %s (valid: --json, --help, -h)", arg)
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	report, err := taskflow.CollectPreflight(root)
	if err != nil {
		return err
	}
	if asJSON {
		if err := taskflow.RenderPreflightJSON(out, report); err != nil {
			return err
		}
	} else {
		taskflow.RenderPreflightText(out, report)
	}
	if report.Verdict == "NOT_READY" {
		return fmt.Errorf("preflight: %d runnable, %d unrunnable, %d blocking",
			report.Runnable, report.Unrunnable, len(report.Blocking))
	}
	return nil
}

// runTaskGate measures the four gate steps and exits with the verdict's own
// code: READY 0, NOT READY 1, UNAVAILABLE 2. A board with no tasks directory
// is refused before any step runs -- there is nothing to measure.
func runTaskGate(ctx context.Context, args []string, out, errOut io.Writer) int {
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		case "--help", "-h":
			fmt.Fprintln(out, taskUsage)
			return 0
		default:
			fmt.Fprintf(errOut, "Error: unknown argument: %s (valid: --json, --help, -h)\n", arg)
			return 2
		}
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(errOut, "Error: %v\n", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(root, taskflow.TasksDir)); err != nil {
		fmt.Fprintln(errOut, "Error: no tasks directory to measure")
		return 2
	}
	outcome, err := taskflow.RunGate(ctx, out, root, asJSON)
	if err != nil {
		fmt.Fprintf(errOut, "Error: %v\n", err)
		return 1
	}
	if outcome.Summary != "" {
		fmt.Fprintf(errOut, "Error: %s\n", outcome.Summary)
	}
	return outcome.ExitCode
}
