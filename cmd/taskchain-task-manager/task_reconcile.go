package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardruntime"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

func taskReconcile(ctx context.Context, args []string, out io.Writer) error {
	dryRun, explicitApply, asJSON := false, false, false
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(out, reconcileHelp)
			return nil
		case "--dry-run":
			dryRun = true
		case "--apply":
			explicitApply = true
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("unknown reconcile argument: %s (valid: --dry-run, --apply, --json, --help, -h)", arg)
		}
	}
	if dryRun && explicitApply {
		return fmt.Errorf("--dry-run and --apply are mutually exclusive")
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if !dryRun {
		source, err := boardruntime.SourceBranch(ctx, root)
		if err != nil {
			return err
		}
		if err := boardruntime.ApplySourceGuard(ctx, root, source); err != nil {
			return fmt.Errorf("reconcile: %w", err)
		}
	}
	result, reconcileErr := boardruntime.Reconcile(ctx, root, taskflow.TasksDirName(), dryRun)
	if asJSON {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "reconcile: dry-run=%t applied=%t\n", result.DryRun, result.Applied)
		for _, card := range result.Cards {
			fmt.Fprintf(out, "  %s %s: %s — %s\n", card.ID, card.Path, card.Action, card.Reason)
		}
	}
	return reconcileErr
}

const reconcileHelp = `Usage: taskchain-task-manager task reconcile [--dry-run | --apply] [--json]

Apply doing-card reconciliation by default. --dry-run writes nothing. --apply is the explicit form of the default. A card moves to blocked or review only on a valid terminal receipt. It never moves to done.

Options:
  --dry-run   Report the transitions without writing
  --apply     Apply the transitions (this is the default)
  --json      Machine-readable output
  --help, -h  Show this help
`
