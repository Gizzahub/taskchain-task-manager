package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/workspace"
)

const workspaceQueryUsage = "query-workspace --manifest <workspace.json> (--card-id ID | --kind intent|batch|iteration --context-id ID --revision N) [--repository NAME] --json"

func runWorkspaceQuery(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", workspaceQueryUsage)
		fmt.Fprintln(out, "Read one card or one exact registered context from explicitly declared repositories.")
		return 0
	}
	flags := flag.NewFlagSet("query-workspace", flag.ContinueOnError)
	flags.SetOutput(errOut)
	manifestPath := flags.String("manifest", "", "strict workspace manifest")
	repository := flags.String("repository", "", "optional repository name")
	cardID := flags.String("card-id", "", "card ID")
	kind := flags.String("kind", "", "intent, batch or iteration")
	contextID := flags.String("context-id", "", "registered context ID")
	revision := flags.String("revision", "", "positive uint32 context revision")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *manifestPath == "" {
		fmt.Fprintln(errOut, "usage:", workspaceQueryUsage)
		return 2
	}
	selector := workspace.Selector{Repository: *repository}
	if *cardID != "" {
		if *kind != "" || *contextID != "" || *revision != "" {
			fmt.Fprintln(errOut, "query-workspace: choose exactly one selector")
			return 2
		}
		selector.CardID = *cardID
	} else {
		if (*kind != "intent" && *kind != "batch" && *kind != "iteration") || *contextID == "" || *revision == "" {
			fmt.Fprintln(errOut, "usage:", workspaceQueryUsage)
			return 2
		}
		rev, err := parseContextRevision(*revision)
		if err != nil {
			fmt.Fprintln(errOut, "query-workspace: revision:", err)
			return 2
		}
		selector.ContextKind = *kind
		selector.ContextID = *contextID
		selector.ContextRevision = rev
	}
	manifest, err := workspace.Load(*manifestPath)
	if err != nil {
		fmt.Fprintln(errOut, "query-workspace: load manifest:", err)
		return 1
	}
	result, err := workspace.Query(manifest, selector)
	if err != nil {
		fmt.Fprintln(errOut, "query-workspace:", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write workspace result:", err)
		return 1
	}
	return 0
}
