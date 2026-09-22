package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"io"
)

func runInspectWorktrees(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("inspect-worktrees", flag.ContinueOnError)
	flags.SetOutput(errOut)
	repo := flags.String("repo", "", "Git repository root")
	board := flags.String("board", "tasks", "existing repository-relative board")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *repo == "" || *board == "" {
		fmt.Fprintln(errOut, "expected inspect-worktrees --repo <root> --board <path> --json")
		return 2
	}
	report, err := githistory.InspectWorktrees(context.Background(), *repo, *board)
	if err != nil {
		fmt.Fprintln(errOut, "inspect worktrees:", err)
		return 1
	}
	if err := outputformat.Encode(out, report); err != nil {
		fmt.Fprintln(errOut, "write worktree inspection:", err)
		return 1
	}
	return 0
}
