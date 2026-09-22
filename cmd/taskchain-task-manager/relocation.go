package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"io"
)

const relocationUsage = "relocate --dir <board> --id <card ID> --source <card path> --target <card path> --owner <owner> [--token <held token>] --request-id <32lowerhex> --expected-sha256 <64lowerhex> [--adopt | --resume] --json"

func runRelocation(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", relocationUsage)
		fmt.Fprintln(out, "Move a card through an explicitly allowed kind edge; preserve identity and category path. --resume never starts a new operation.")
		return 0
	}
	flags := flag.NewFlagSet("relocate", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	id := flags.String("id", "", "card ID")
	source := flags.String("source", "", "board-relative source")
	target := flags.String("target", "", "board-relative target")
	owner := flags.String("owner", "", "operation owner")
	token := flags.String("token", "", "existing held claim token")
	requestID := flags.String("request-id", "", "unique retry identifier")
	digest := flags.String("expected-sha256", "", "SHA-256 of original bytes")
	adopt := flags.Bool("adopt", false, "adopt storage v2 after upgrading all writers")
	resume := flags.Bool("resume", false, "resume the exact recorded request")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" || *id == "" || *source == "" || *target == "" || *owner == "" || *requestID == "" || *digest == "" || (*adopt && *resume) {
		fmt.Fprintln(errOut, "expected", relocationUsage)
		return 2
	}
	req := taskstore.RelocationRequest{ID: *id, Owner: *owner, Token: *token, RequestID: *requestID, Source: *source, Target: *target, ExpectedSHA256: *digest}
	var result taskstore.RelocationResult
	var err error
	if *resume {
		result, err = taskstore.RecoverRelocation(*dir, req)
	} else {
		result, err = taskstore.Relocate(*dir, req, *adopt)
	}
	if err != nil {
		fmt.Fprintln(errOut, "relocate:", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write result (relocation may already be recorded; retry the identical request):", err)
		return 1
	}
	return 0
}
