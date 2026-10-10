package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

func taskValidate(ctx context.Context, args []string, out io.Writer) error {
	all := false
	for _, arg := range args {
		switch arg {
		case "--all":
			all = true
		case "--help", "-h":
			fmt.Fprintln(out, taskUsage)
			return nil
		default:
			return usagef("unknown argument: %s (valid: --all, --help, -h)", arg)
		}
	}
	if !all {
		// TODO(validation-gate bundle): the single-card `validate <path>` mode
		// and the gate receipts belong to that bundle; until it lands the noun
		// refuses rather than half-implementing the contract.
		return usagef("task validate takes --all")
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	tasksDir, err := taskflow.ResolveTasksDir()
	if err != nil {
		// The reference stamps its tool line before it reads TASKS_DIR, so a
		// refused redirection is still announced by the tool that refused it.
		fmt.Fprintln(out, taskflow.ReferenceStamp)
		return err
	}
	invalid, err := taskflow.RenderValidateAllIn(ctx, out, root, tasksDir)
	if err != nil {
		return err
	}
	if invalid > 0 {
		return fmt.Errorf("%d task(s) failed validation", invalid)
	}
	return nil
}
