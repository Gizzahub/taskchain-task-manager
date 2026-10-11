package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

func taskGateEvidence(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, `taskchain-task-manager task gate-evidence — bind a clean PASS log to the current commit

USAGE:
  taskchain-task-manager task gate-evidence [--log PATH]

Reads the log declared by ce-tasks.yaml gate.log, or --log, and writes a
durable receipt only for a clean PASS that finished after the current commit
and binds HEAD, tree, and Change-Id.`)
		return nil
	}
	logFlag := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--log":
			if i+1 >= len(args) || args[i+1] == "" {
				return usagef("--log needs a path")
			}
			i++
			logFlag = args[i]
		case strings.HasPrefix(arg, "--log="):
			logFlag = strings.TrimPrefix(arg, "--log=")
			if logFlag == "" {
				return usagef("--log needs a path")
			}
		default:
			return usagef("unknown argument %s (valid: --log PATH, --help, -h)", arg)
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	block, dest, err := taskflow.PrepareGateEvidence(ctx, root, logFlag)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "```\n%s```\n\ndurable copy: %s\n", block, dest)
	return nil
}
