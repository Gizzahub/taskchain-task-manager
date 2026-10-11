package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

func taskChangeID(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, `taskchain-task-manager task change-id — print the branch Change-Id

USAGE:
  taskchain-task-manager task change-id

Prints the single taskchain-gate/<32 hex> Change-Id declared by commits in
the source-branch range merge-base..HEAD.`)
		return nil
	}
	if len(args) > 0 {
		return usagef("unknown argument %s (valid: --help, -h)", args[0])
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	id, err := taskflow.BranchChangeID(ctx, root)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, id)
	return nil
}
