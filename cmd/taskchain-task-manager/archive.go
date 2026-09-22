package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"io"
)

const archiveUsage = "archive --dir <board> --id <card ID> --source <card path> --rules <archive.yaml> --owner <owner> [--token <held token>] --request-id <32lowerhex> --expected-sha256 <64lowerhex> [--operation archive|supersede|force] [--assertion <reason>] [--adopt | --resume] --json"

func runArchive(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", archiveUsage)
		fmt.Fprintln(out, "Archive a card without losing dependency completion. Force and supersede do not certify completion. --resume only recovers an exact recorded request.")
		return 0
	}
	f := flag.NewFlagSet("archive", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("dir", "", "task board directory")
	id := f.String("id", "", "exact card ID spelling")
	source := f.String("source", "", "board-relative source card")
	rules := f.String("rules", "", "explicit archive admission rules")
	owner := f.String("owner", "", "operation owner")
	token := f.String("token", "", "existing held claim token")
	request := f.String("request-id", "", "unique retry identifier")
	digest := f.String("expected-sha256", "", "SHA-256 of original bytes")
	operation := f.String("operation", string(outputvocab.ArchiveOp), "archive, supersede, or force")
	assertion := f.String("assertion", "", "required reason for force")
	adopt := f.Bool("adopt", false, "adopt storage v3 after upgrading every writer")
	resume := f.Bool("resume", false, "recover the exact recorded request")
	asJSON := f.Bool("json", false, "write JSON")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || !*asJSON || *dir == "" || *id == "" || *source == "" || *rules == "" || *owner == "" || *request == "" || *digest == "" || (*adopt && *resume) {
		fmt.Fprintln(errOut, "expected", archiveUsage)
		return 2
	}
	raw, err := readValidationInput(*rules, 64<<10)
	if err != nil {
		fmt.Fprintln(errOut, "archive rules:", err)
		return 1
	}
	req := taskstore.ArchiveRequest{ID: *id, Owner: *owner, Token: *token, RequestID: *request, Source: *source, ExpectedSHA256: *digest, Operation: *operation, Assertion: *assertion, Rules: raw}
	var result taskstore.ArchiveResult
	if *resume {
		result, err = taskstore.RecoverArchive(*dir, req)
	} else {
		result, err = taskstore.Archive(*dir, req, *adopt)
	}
	if err != nil {
		fmt.Fprintln(errOut, "archive:", err)
		fmt.Fprintln(errOut, "Preserve journals and locks. Retry the identical request; use --resume for a recorded operation, or --adopt to resume incomplete protocol adoption.")
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write result (archive may already be recorded; retry the identical request):", err)
		return 1
	}
	return 0
}
