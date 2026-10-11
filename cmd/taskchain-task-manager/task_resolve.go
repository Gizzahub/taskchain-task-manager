package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

const resolveUsage = "usage: taskchain-task-manager task resolve <issue-file> fixed [--by TASK-N] | duplicate [--by ISSUE-N] | wontfix | invalid"

func taskResolve(ctx context.Context, args []string, out io.Writer) error {
	var positional []string
	var by string
	byProvided := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			fmt.Fprintln(out, resolveUsage)
			return nil
		case "--by":
			if i+1 >= len(args) {
				return fmt.Errorf("flag --by needs a value")
			}
			byProvided = true
			by = args[i+1]
			i++
		case "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s (valid: --by, --help, -h)", args[i])
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 2 {
		return fmt.Errorf("%s", resolveUsage)
	}
	if len(positional) > 2 {
		return fmt.Errorf("unexpected argument: %s (ce task resolve takes two arguments)", positional[2])
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	result, err := taskflow.ResolveIssueWithByFlag(ctx, root, positional[0], positional[1], by, byProvided)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "✅ Resolved: %s\n   resolution: %s\n   resolved-at: %s\n   resolution-summary: %s\n", result.Path, result.Resolution, result.ResolvedAt, result.Summary)
	return nil
}
