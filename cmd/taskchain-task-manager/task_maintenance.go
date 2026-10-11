package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardruntime"
)

func taskMaintenance(ctx context.Context, args []string, out io.Writer) error {
	apply := false
	var filtered []string
	for _, arg := range args {
		switch arg {
		case "plan":
			apply = false
		case "apply":
			apply = true
		default:
			filtered = append(filtered, arg)
		}
	}
	asJSON := false
	var planFile, filesDir string
	for i := 0; i < len(filtered); i++ {
		arg := filtered[i]
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprint(out, maintenanceHelp)
			return nil
		case arg == "--apply":
			apply = true
		case arg == "--json":
			asJSON = true
		case arg == "--files":
			if i+1 >= len(filtered) {
				return fmt.Errorf("--files requires a directory argument")
			}
			i++
			filesDir = filtered[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag: %s", arg)
			}
			if planFile != "" {
				return fmt.Errorf("unexpected multiple plan files: %s, %s", planFile, arg)
			}
			planFile = arg
		}
	}
	if planFile == "" {
		return fmt.Errorf("maintenance plan file argument is required")
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	result, err := boardruntime.Maintain(ctx, root, boardruntime.MaintenanceRequest{
		PlanFile: planFile, Apply: apply, FilesDir: filesDir,
	})
	committed := errors.Is(err, boardruntime.ErrCommitted)
	if err != nil && !committed {
		return err
	}
	// Past this point the board exchange happened (or was only previewed).
	// An applied exchange must never look retryable, so every later failure
	// of an applied run exits 3.
	applied := committed || result.Committed
	writeErr := writeMaintenanceResult(out, result, asJSON)
	switch {
	case applied && err != nil && writeErr != nil:
		return &runExitError{code: 3, reason: fmt.Sprintf("applied, do not rerun: %v; write result: %v", err, writeErr)}
	case applied && err != nil:
		return &runExitError{code: 3, reason: fmt.Sprintf("applied, do not rerun: %v", err)}
	case applied && writeErr != nil:
		return &runExitError{code: 3, reason: fmt.Sprintf("applied, do not rerun: write result: %v", writeErr)}
	}
	return writeErr
}

func writeMaintenanceResult(out io.Writer, result boardruntime.MaintenanceResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	mode := "Preview"
	if result.Committed {
		mode = "Applied"
	}
	lines := []string{
		fmt.Sprintf("Board maintenance [%s]:", mode),
		fmt.Sprintf("  Plan Digest: %s", result.PlanDigest),
		fmt.Sprintf("  Cards:       %d", result.Cards),
		fmt.Sprintf("  Mappings:   %d", result.Mappings),
	}
	if result.Committed {
		lines = append(lines, fmt.Sprintf("  Receipt:     %s", result.ReceiptPath))
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

const maintenanceHelp = `Usage: taskchain-task-manager task maintenance [plan|apply] <plan-file> [--apply] [--files DIR] [--json]

Verify or apply a frozen board-maintenance plan under the mutation lock. The default is a preview. apply or --apply exchanges the board directory atomically. A plan file cannot be named plan or apply.

Options:
  --apply       Apply the plan
  --files DIR   Directory of replacement card bytes
  --json        Machine-readable output
  --help, -h    Show this help
`
