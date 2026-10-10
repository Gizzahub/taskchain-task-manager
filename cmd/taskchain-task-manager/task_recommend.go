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

func taskRecommend(ctx context.Context, args []string, out io.Writer) error {
	_ = ctx
	opts := taskflow.RecommendOptions{Limit: 5}
	asJSON := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--help" || args[i] == "-h" {
			fmt.Fprint(out, recommendHelp)
			return nil
		}
		switch args[i] {
		case "--category":
			value, err := spacedFlag(args, i)
			if err != nil {
				return err
			}
			opts.Category = value
			i++
		case "--max-effort":
			value, err := spacedFlag(args, i)
			if err != nil {
				return err
			}
			if !knownEffort(value) {
				return fmt.Errorf("unknown --max-effort value: %s (valid: XS, S, M, L, XL)", value)
			}
			opts.MaxEffort = value
			i++
		case "--limit":
			value, err := spacedFlag(args, i)
			if err != nil {
				return err
			}
			n, convErr := strconv.Atoi(value)
			if convErr != nil || n < 1 {
				return fmt.Errorf("--limit needs a positive whole number, got: %s", value)
			}
			opts.Limit = n
			i++
		case "--json":
			asJSON = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s (valid: --category, --max-effort, --limit, --json, --help, -h)", args[i])
			}
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	report, err := taskflow.Recommend(root, opts)
	if err != nil {
		if strings.HasPrefix(err.Error(), "open tasks tree:") {
			return usagef("%s", err.Error())
		}
		return err
	}
	if asJSON {
		return taskflow.RenderRecommendJSON(out, report)
	}
	taskflow.RenderRecommendText(out, report)
	return nil
}

func spacedFlag(args []string, i int) (string, error) {
	if i+1 >= len(args) {
		return "", fmt.Errorf("flag %s needs a value", args[i])
	}
	return args[i+1], nil
}

func knownEffort(value string) bool {
	switch strings.ToUpper(value) {
	case "XS", "S", "M", "L", "XL":
		return true
	}
	return false
}

const recommendHelp = `Usage: taskchain-task-manager task recommend [--category C] [--max-effort E] [--limit N] [--json]

Score the pending cards and print the ones to start next, highest first. A
card whose depends-on predecessor is still open drops out of the queue.
When the pending pool is empty, the report says why. Exit stays 0 either
way, including an empty board. A tasks tree that cannot be opened exits 2.

Options:
  --category C    Only cards in category C (exact match)
  --max-effort E  Skip cards larger than E (XS, S, M, L, XL)
  --limit N       Print at most N cards (default 5)
  --json          Machine-readable output
  --help, -h      Show this help

A value follows its flag as a separate word (--limit 3). The equals form is
not accepted here and is reported as an unknown flag.
`
