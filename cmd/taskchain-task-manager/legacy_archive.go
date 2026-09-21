package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const legacyArchiveUsage = "adopt-legacy-archive --dir <board> --id <card ID> --source <archived path> --rules <archive.yaml> --owner <owner> [--token <held token>] --request-id <32lowerhex> --expected-sha256 <64lowerhex> --expected-mode <octal permissions> --assertion <operator statement> [--approve-completion] [--adopt | --resume] --json"

func runLegacyArchive(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", legacyArchiveUsage)
		fmt.Fprintln(out, "Record an existing archived card without moving it. Completion requires explicit operator approval; it is not independently verified workflow completion.")
		return 0
	}
	f := flag.NewFlagSet("adopt-legacy-archive", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("dir", "", "task board directory")
	id := f.String("id", "", "exact card ID spelling")
	source := f.String("source", "", "board-relative archived card")
	rules := f.String("rules", "", "explicit archive rules")
	owner := f.String("owner", "", "operation owner")
	token := f.String("token", "", "existing held claim token")
	request := f.String("request-id", "", "unique retry identifier")
	digest := f.String("expected-sha256", "", "SHA-256 of current card bytes")
	mode := f.String("expected-mode", "", "current permission bits in octal, for example 0644")
	assertion := f.String("assertion", "", "operator statement, not a quality certification")
	approve := f.Bool("approve-completion", false, "explicitly approve legacy TASK dependency completion")
	adopt := f.Bool("adopt", false, "adopt storage v3 after upgrading all writers")
	resume := f.Bool("resume", false, "recover only the exact recorded request")
	asJSON := f.Bool("json", false, "write JSON")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	bits, modeErr := strconv.ParseUint(*mode, 8, 32)
	if f.NArg() != 0 || !*asJSON || *dir == "" || *id == "" || *source == "" || *rules == "" || *owner == "" || *request == "" || *digest == "" || *assertion == "" || modeErr != nil || bits == 0 || bits > 0777 || (*adopt && *resume) {
		fmt.Fprintln(errOut, "expected", legacyArchiveUsage)
		return 2
	}
	raw, err := readValidationInput(*rules, 64<<10)
	if err != nil {
		fmt.Fprintln(errOut, "legacy archive rules:", err)
		return 1
	}
	req := taskstore.LegacyArchiveRequest{ArchiveRequest: taskstore.ArchiveRequest{ID: *id, Owner: *owner, Token: *token, RequestID: *request, Source: *source, ExpectedSHA256: *digest, Operation: string(outputvocab.LegacyAdoption), Assertion: *assertion, Rules: raw}, ExpectedMode: uint32(bits), ApproveCompletion: *approve}
	var result taskstore.ArchiveResult
	if *resume {
		result, err = taskstore.RecoverLegacyArchive(*dir, req)
	} else {
		result, err = taskstore.AdoptLegacyArchive(*dir, req, *adopt)
	}
	if err != nil {
		fmt.Fprintln(errOut, "legacy archive:", err)
		fmt.Fprintln(errOut, "Preserve journals and locks. Retry the identical request including mode and completion approval; --resume never starts a new operation.")
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write result (legacy adoption may already be recorded; retry the identical request):", err)
		return 1
	}
	return 0
}
