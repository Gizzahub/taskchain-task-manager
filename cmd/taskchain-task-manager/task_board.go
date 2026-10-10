package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

func taskBoard(ctx context.Context, args []string, out io.Writer) error {
	_ = ctx
	asJSON := false
	staleHours := taskflow.DefaultStaleHours()
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--help" || args[i] == "-h":
			fmt.Fprint(out, boardHelp)
			return nil
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--stale-hours":
			if i+1 >= len(args) {
				return fmt.Errorf("flag --stale-hours needs a value")
			}
			n, err := taskflow.ParseStaleHours(args[i+1])
			if err != nil {
				return err
			}
			staleHours = n
			i++
		case strings.HasPrefix(args[i], "--stale-hours="):
			n, err := taskflow.ParseStaleHours(strings.TrimPrefix(args[i], "--stale-hours="))
			if err != nil {
				return err
			}
			staleHours = n
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s (valid: --json, --stale-hours, --help, -h)", args[i])
			}
			return usagef("usage: taskchain-task-manager task board [--json] [--stale-hours N]")
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	cards, mtimes, err := taskflow.LoadBoardCards(root)
	if err != nil {
		return err
	}
	after := time.Duration(staleHours) * time.Hour
	zones := taskflow.ClassifyBoard(cards, mtimes, time.Now(), after)
	plans := taskflow.DerivePlanInventory(cards)
	if asJSON {
		return taskflow.RenderBoardJSON(out, zones, staleHours, plans)
	}
	taskflow.RenderBoardText(out, zones, staleHours, plans)
	return nil
}

const boardHelp = `Usage: taskchain-task-manager task board [--json] [--stale-hours N]

Show the tasks/ claim board grouped by zone. doing/ claims older than
--stale-hours (default 24; 0 disables) are marked stale. Nothing is written.
A positional argument is a usage error.

Options:
  --json            Machine-readable output
  --stale-hours N   Flag doing/ claims older than N hours (default 24; 0 disables)
                    Also accepted as: --stale-hours=N
  --help, -h        Show this help
`
