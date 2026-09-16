package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

type reservationIDs []string

func (r *reservationIDs) String() string         { return fmt.Sprint([]string(*r)) }
func (r *reservationIDs) Set(value string) error { *r = append(*r, value); return nil }

func runReservations(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("reserve-ids", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "tasks", "task board directory")
	var ids reservationIDs
	flags.Var(&ids, "id", "canonical task ID (repeatable)")
	adopt := flags.Bool("adopt", false, "initialize a missing ledger from observed IDs")
	jsonOut := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*jsonOut || (*adopt == false && len(ids) == 0) || *dir == "" {
		fmt.Fprintln(errOut, "expected --dir <board> [--id TASK-N ...] [--adopt] --json")
		return 2
	}
	result, err := taskstore.ReserveIDs(*dir, ids, *adopt)
	if err != nil {
		fmt.Fprintln(errOut, "reserve-ids:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write result (reservation may be applied; retry the exact arguments):", err)
		return 1
	}
	return 0
}
