package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"io"
)

const planProgressUsage = "plan-progress --dir <board> --id <plan ID> --rules <archive.yaml> --json"

func runPlanProgress(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", planProgressUsage)
		fmt.Fprintln(out, "Count a plan's declared children against the board's completion index. The count is derived on every read and is never written back to the card; a consumer that needs it in a file owns that write and owns reporting whether it succeeded.")
		return 0
	}
	f := flag.NewFlagSet("plan-progress", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("dir", "", "task board directory")
	id := f.String("id", "", "exact plan card ID spelling")
	rules := f.String("rules", "", "archive admission rules naming the children field")
	asJSON := f.Bool("json", false, "write JSON")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || !*asJSON || *dir == "" || *id == "" || *rules == "" {
		fmt.Fprintln(errOut, "expected", planProgressUsage)
		return 2
	}
	raw, err := readValidationInput(*rules, 64<<10)
	if err != nil {
		fmt.Fprintln(errOut, "plan progress rules:", err)
		return 1
	}
	result, err := taskstore.ReadPlanProgress(*dir, taskstore.PlanProgressRequest{ID: *id, Rules: raw})
	if err != nil {
		fmt.Fprintln(errOut, "plan-progress:", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write plan progress:", err)
		return 1
	}
	return 0
}
