package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

const validateUsage = "usage: taskchain-task-manager task validate [file...|--all|--staged] [--require-cards] [--base COMMIT] [--json]"

const validateFlags = "--all, --staged, --base, --json, --require-cards, --help, -h"

type validateOpts struct {
	all, staged, json, requireCards, help bool
	base                                  string
	baseSet                               bool
	files                                 []string
}

func taskValidate(ctx context.Context, args []string, out, errOut io.Writer) error {
	opts, err := parseTaskValidateArgs(args, out)
	if err != nil || opts.help {
		return err
	}
	if opts.staged {
		return taskValidateStaged(ctx, opts.json, out, errOut)
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	tasksDir, err := taskflow.ResolveTasksDir()
	if err != nil {
		fmt.Fprintln(out, taskflow.ReferenceStamp)
		return err
	}
	if opts.all {
		return taskValidateAll(ctx, out, root, tasksDir, opts.requireCards)
	}
	fmt.Fprintln(out, taskflow.ReferenceStamp)
	if err := taskflow.CheckConfigYAML(root); err != nil {
		return usagef("%s", err.Error())
	}
	tree, err := taskflow.SelectCriterionTree(ctx, root, opts.base)
	if err != nil {
		return usagef("%s", err.Error())
	}
	defer tree.Cleanup(errOut)
	fmt.Fprintf(out, "Criterion tree: %s\n", tree.Description)
	invalid, firstErrors, err := taskflow.ValidateNamed(ctx, out, root, tasksDir, opts.files, tree)
	if err != nil {
		return err
	}
	if invalid == 0 {
		return nil
	}
	if len(opts.files) == 1 {
		return fmt.Errorf("validation failed with %d error(s)", firstErrors)
	}
	return fmt.Errorf("%d task(s) failed validation", invalid)
}

func taskValidateAll(ctx context.Context, out io.Writer, root, tasksDir string, requireCards bool) error {
	if requireCards {
		live, err := taskflow.FindCardsIn(root, tasksDir, true)
		if err != nil {
			return err
		}
		if len(live) == 0 && taskflow.CorpusEmpty(root, tasksDir) {
			fmt.Fprintln(out, taskflow.ReferenceStamp)
			fmt.Fprintln(out, "📋 Validating all tasks...")
			fmt.Fprintln(out)
			return fmt.Errorf("no task files found under %s/, but --require-cards was given; that tree is empty or unreachable", tasksDir)
		}
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

func taskValidateStaged(ctx context.Context, jsonOut bool, out, errOut io.Writer) error {
	root, err := taskflowRepoRoot(ctx)
	if err != nil {
		return usagef("%s", err.Error())
	}
	snapshot, err := taskflow.MaterializeIndex(ctx, root)
	if err != nil {
		return usagef("%s", err.Error())
	}
	defer func() {
		if removeErr := os.RemoveAll(snapshot); removeErr != nil {
			fmt.Fprintf(errOut, "warning: remove staged task snapshot: %v\n", removeErr)
		}
	}()
	if !jsonOut {
		fmt.Fprintln(out, taskflow.ReferenceStamp)
	}
	findings, warnings, err := taskflow.ValidateStaged(ctx, snapshot)
	if err != nil {
		return usagef("%s", err.Error())
	}
	if err := taskflow.RenderStaged(out, findings, warnings, jsonOut); err != nil {
		return usagef("%s", err.Error())
	}
	if len(findings) != 0 {
		return fmt.Errorf("%d staged task finding(s)", len(findings))
	}
	return nil
}

func taskflowRepoRoot(ctx context.Context) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	top, err := taskflow.ShowToplevel(ctx, root)
	if err != nil {
		return "", fmt.Errorf("staged validation requires a Git repository: %s", err.Error())
	}
	return top, nil
}

func parseTaskValidateArgs(args []string, out io.Writer) (validateOpts, error) {
	var opts validateOpts
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			fmt.Fprintln(out, validateUsage)
			return validateOpts{help: true}, nil
		case "--all":
			opts.all = true
		case "--staged":
			opts.staged = true
		case "--json":
			opts.json = true
		case "--require-cards":
			opts.requireCards = true
		case "--base":
			if opts.baseSet {
				return opts, usagef("--base may be supplied only once")
			}
			value, err := flagValue(args, i)
			if err != nil {
				return opts, usagef("%s", err.Error())
			}
			i++
			opts.base, opts.baseSet = value, true
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, usagef("unknown flag: %s (valid: %s)", arg, validateFlags)
			}
			if strings.TrimSpace(arg) == "" {
				return opts, usagef("empty path argument (an unset shell variable?); name a task file or use --all")
			}
			opts.files = append(opts.files, arg)
		}
	}
	if opts.baseSet && opts.staged {
		return opts, usagef("--base cannot be combined with --staged")
	}
	if opts.json && !opts.staged {
		return opts, usagef("--json is only available with --staged")
	}
	if opts.requireCards && !opts.all {
		return opts, usagef("--require-cards is only available with --all")
	}
	if opts.staged && (opts.all || len(opts.files) > 0) {
		return opts, usagef("--staged validates the Git index snapshot; it cannot be combined with --all or named files")
	}
	if opts.all && len(opts.files) > 0 {
		return opts, usagef("--all validates the whole tasks tree; it cannot be combined with named files (%s)", strings.Join(opts.files, ", "))
	}
	if !opts.all && !opts.staged && len(opts.files) == 0 {
		return opts, usagef("specify a task file or use --all")
	}
	return opts, nil
}
