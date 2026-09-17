package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const archiveCapacityUsage = "adopt-archive-capacity --dir <board> --upgrade-id <32lowerhex> (--adopt | --resume) --json"

func runArchiveCapacity(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", archiveCapacityUsage)
		fmt.Fprintln(out, "Adopt archive journal schema 2 with an exact upgrade ID. --resume only recovers the recorded local adoption.")
		return 0
	}
	f := flag.NewFlagSet("adopt-archive-capacity", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("dir", "", "task board directory")
	upgradeID := f.String("upgrade-id", "", "exact 32 lowercase hex adoption identifier")
	adopt := f.Bool("adopt", false, "start a new protocol 5 capacity adoption")
	resume := f.Bool("resume", false, "recover the exact recorded capacity adoption")
	asJSON := f.Bool("json", false, "write JSON")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || *dir == "" || *upgradeID == "" || !*asJSON || *adopt == *resume {
		fmt.Fprintln(errOut, "expected", archiveCapacityUsage)
		return 2
	}
	result, err := taskstore.ArchiveCapacity(*dir, *upgradeID, *adopt, *resume)
	if err != nil {
		fmt.Fprintln(errOut, "adopt archive capacity:", err)
		fmt.Fprintln(errOut, "Preserve the adoption journal and payload. Retry with --resume and the identical upgrade ID after restoring any reported exact state.")
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write result (capacity adoption may already be complete; retry with --resume):", err)
		return 1
	}
	return 0
}
