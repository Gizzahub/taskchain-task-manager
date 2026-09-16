package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const repairStatusUsage = "repair-status --dir <board> --id TASK-N --path <board-relative card path> --owner <owner> [--token <held token>] --request-id <32lowerhex> --expected-sha256 <64lowerhex> [--adopt | --resume] --json"

func runRepairStatus(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", repairStatusUsage)
		fmt.Fprintln(out, "Repair one canonical workflow Status cell using the original-bytes digest; --resume only resumes a recorded request.")
		return 0
	}
	flags := flag.NewFlagSet("repair-status", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	id := flags.String("id", "", "canonical task ID")
	path := flags.String("path", "", "board-relative card path")
	owner := flags.String("owner", "", "repair owner")
	token := flags.String("token", "", "optional held claim token")
	requestID := flags.String("request-id", "", "unique repair retry identifier (32 lowercase hex)")
	expectedSHA256 := flags.String("expected-sha256", "", "SHA-256 digest of the original card bytes")
	adopt := flags.Bool("adopt", false, "adopt the storage protocol after upgrading all writers")
	resume := flags.Bool("resume", false, "resume the same recorded repair request")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" || *id == "" || *path == "" || *owner == "" || *requestID == "" || *expectedSHA256 == "" || (*adopt && *resume) {
		fmt.Fprintln(errOut, "expected", repairStatusUsage)
		return 2
	}
	req := taskstore.RepairRequest{
		ID:             *id,
		Owner:          *owner,
		Token:          *token,
		RequestID:      *requestID,
		Path:           *path,
		ExpectedSHA256: *expectedSHA256,
	}
	var result taskstore.RepairResult
	var err error
	if *resume {
		result, err = taskstore.RecoverStatusRepair(*dir, req)
	} else {
		result, err = taskstore.RepairStatus(*dir, req, *adopt)
	}
	if err != nil {
		fmt.Fprintln(errOut, "repair-status:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write result (repair may already be recorded; retry the identical request):", err)
		return 1
	}
	return 0
}
