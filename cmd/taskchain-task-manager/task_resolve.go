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

const resolveUsage = "usage: ce task resolve <issue-file> fixed [--by TASK-N] | duplicate [--by ISSUE-N] | wontfix | invalid"

func taskResolve(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, resolveUsage)
		return nil
	}
	var positionals []string
	bySet := false
	byValue := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "--by" {
			bySet = true
			if i+1 >= len(args) {
				continue
			}
			next := args[i+1]
			if next != "--" && strings.HasPrefix(next, "-") {
				continue
			}
			i++
			byValue = next
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("unknown flag: %s (valid: --by, --help, -h)", arg)
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) < 2 {
		return fmt.Errorf("%s", resolveUsage)
	}
	if len(positionals) > 2 {
		return fmt.Errorf("unexpected argument: %s (ce task resolve takes two arguments)", positionals[2])
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	result, err := taskflow.Resolve(ctx, root, taskflow.ResolveRequest{
		Path:       positionals[0],
		Resolution: positionals[1],
		By:         byValue,
		BySet:      bySet,
		Now:        time.Now(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "✅ Resolved: %s\n   resolution: %s\n   resolved-at: %s\n   resolution-summary: %s\n",
		result.Path, result.Resolution, result.ResolvedAt, result.Summary)
	return nil
}
