package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"io"
)

func runEnableShared(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("enable-shared", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "existing Git task board")
	all := flags.Bool("all-worktrees", false, "acknowledge converting this board in every registered worktree")
	resume := flags.Bool("resume", false, "resume a recorded interrupted activation")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *dir == "" || !*all || !*asJSON {
		fmt.Fprintln(errOut, "expected enable-shared --dir <board> --all-worktrees [--resume] --json; stop all board writers before activation")
		return 2
	}
	result, err := taskstore.EnableShared(*dir, *resume)
	if err != nil {
		fmt.Fprintln(errOut, "enable shared IDs:", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write shared activation result (activation may be applied; inspect/retry):", err)
		return 1
	}
	return 0
}
