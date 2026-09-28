package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/workspace"
)

const workspaceContextUsage = "workspace-context --manifest <workspace.json> --card-id ID [--card-id ID ...] --json"

type repeatedStrings []string

func (values *repeatedStrings) String() string { return strings.Join(*values, ",") }

func (values *repeatedStrings) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func runWorkspaceContext(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", workspaceContextUsage)
		fmt.Fprintln(out, "Read a bounded batch of card IDs without creating board locks.")
		return 0
	}
	flags := flag.NewFlagSet("workspace-context", flag.ContinueOnError)
	flags.SetOutput(errOut)
	manifestPath := flags.String("manifest", "", "strict workspace inventory manifest")
	var cardIDs repeatedStrings
	flags.Var(&cardIDs, "card-id", "card ID to include; repeat for a batch")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *manifestPath == "" || len(cardIDs) == 0 {
		fmt.Fprintln(errOut, "usage:", workspaceContextUsage)
		return 2
	}
	manifest, err := workspace.Load(*manifestPath)
	if err != nil {
		fmt.Fprintln(errOut, "workspace-context: load manifest:", err)
		return 1
	}
	result, err := workspace.QuerySnapshot(manifest, cardIDs)
	if err != nil {
		fmt.Fprintln(errOut, "workspace-context:", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write workspace context:", err)
		return 1
	}
	return 0
}
