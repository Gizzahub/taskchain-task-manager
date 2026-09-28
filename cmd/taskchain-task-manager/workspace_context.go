package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/workspacecontext"
)

func runWorkspaceContext(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("workspace-context", flag.ContinueOnError)
	flags.SetOutput(errOut)
	jsonOutput := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 || !*jsonOutput {
		fmt.Fprintln(errOut, "expected workspace-context <manifest> --json")
		return 2
	}
	f, err := os.Open(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(errOut, "read workspace manifest:", err)
		return 1
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, workspacecontext.MaxManifestBytes+1))
	if err != nil {
		fmt.Fprintln(errOut, "read workspace manifest:", err)
		return 1
	}
	manifest, err := workspacecontext.DecodeManifest(raw)
	if err != nil {
		fmt.Fprintln(errOut, "decode workspace manifest:", err)
		return 1
	}
	result, err := workspacecontext.Lookup(context.Background(), manifest)
	if err != nil {
		fmt.Fprintln(errOut, "workspace context:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write workspace context:", err)
		return 1
	}
	return 0
}
