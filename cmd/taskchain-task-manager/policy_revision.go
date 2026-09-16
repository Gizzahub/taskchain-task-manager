package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const policyRevisionUsage = "revise-policy <file> --dir <board> --expected-authority <32hex> --expected-digest <64hex> [--all-worktrees] [--resume] --json"

func runPolicyRevision(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", policyRevisionUsage)
		fmt.Fprintln(out, "Replace the active policy with an explicit compare-and-swap revision.")
		fmt.Fprintln(out, "The first request must identify the active policy. Resume and result confirmation retain those original expected values.")
		fmt.Fprintln(out, "Shared namespaces require --all-worktrees; --resume never starts a new revision.")
		return 0
	}
	if len(args) < 3 || args[1] == "" {
		fmt.Fprintln(errOut, "usage:", policyRevisionUsage)
		return 2
	}
	flags := flag.NewFlagSet("revise-policy", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	authority := flags.String("expected-authority", "", "current policy authority ID")
	digest := flags.String("expected-digest", "", "current policy digest")
	all := flags.Bool("all-worktrees", false, "acknowledge revision in every registered worktree")
	resume := flags.Bool("resume", false, "resume the exact recorded revision")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || args[1] == "" || !*asJSON || *dir == "" || *authority == "" || *digest == "" {
		fmt.Fprintln(errOut, "expected", policyRevisionUsage)
		return 2
	}
	raw, err := readValidationInput(args[1], 64<<10)
	if err != nil {
		fmt.Fprintln(errOut, "read policy:", err)
		return 1
	}
	result, err := taskstore.RevisePolicy(*dir, raw, taskstore.PolicyRevisionOptions{
		ExpectedAuthorityID: *authority,
		ExpectedDigest:      *digest,
		Resume:              *resume,
		AllWorktrees:        *all,
	})
	if err != nil {
		fmt.Fprintln(errOut, "revise policy:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write policy result (revision may already be completed; retry the identical policy and original expected authority/digest):", err)
		return 1
	}
	return 0
}
