package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func runPolicyActivation(args []string, out, errOut io.Writer) int {
	const usage = "activate-policy <file> --dir <board> [--all-worktrees] [--resume] --json"
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", usage)
		fmt.Fprintln(out, "Activate one immutable policy after upgrading and stopping all board writers.")
		fmt.Fprintln(out, "Initial shared activation and its resume require --all-worktrees; join and completed replay do not.")
		return 0
	}
	if len(args) < 3 || args[1] == "" {
		fmt.Fprintln(errOut, "usage:", usage)
		return 2
	}
	flags := flag.NewFlagSet("activate-policy", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	all := flags.Bool("all-worktrees", false, "acknowledge initial activation in every registered worktree")
	resume := flags.Bool("resume", false, "resume the same recorded activation or join")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *dir == "" || !*asJSON {
		fmt.Fprintln(errOut, "expected", usage)
		return 2
	}
	raw, err := readValidationInput(args[1], 64<<10)
	if err != nil {
		fmt.Fprintln(errOut, "read policy:", err)
		return 1
	}
	result, err := taskstore.ActivatePolicy(*dir, raw, taskstore.PolicyActivationOptions{Resume: *resume, AllWorktrees: *all})
	if err != nil {
		fmt.Fprintln(errOut, "activate policy:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write policy result (activation may already be completed; retry the identical policy):", err)
		return 1
	}
	return 0
}
