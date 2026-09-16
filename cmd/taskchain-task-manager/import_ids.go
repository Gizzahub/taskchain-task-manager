package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func runImportIDs(args []string, out, errOut io.Writer) int {
	return runImportIDsWithScan(args, out, errOut, githistory.Scan)
}

func runImportIDsWithScan(args []string, out, errOut io.Writer, scan func(context.Context, string, string) (githistory.Report, error)) int {
	flags := flag.NewFlagSet("import-ids", flag.ContinueOnError)
	flags.SetOutput(errOut)
	repo := flags.String("repo", "", "source Git repository root")
	board := flags.String("board", "tasks", "repository-relative historical board path")
	dir := flags.String("dir", "", "target board for permanent reservations")
	adopt := flags.Bool("adopt", false, "explicitly adopt target missing ID ledger")
	preview := flags.Bool("preview", false, "scan only, no target mutation")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *repo == "" || *board == "" || (*preview && (*dir != "" || *adopt)) || (!*preview && *dir == "") {
		fmt.Fprintln(errOut, "expected import-ids --repo <root> --board <path> (--preview | --dir <target> [--adopt]) --json")
		return 2
	}
	report, err := scan(context.Background(), *repo, *board)
	if err != nil {
		fmt.Fprintln(errOut, "scan ID history:", err)
		return 1
	}
	result := struct {
		History     githistory.Report            `json:"history"`
		Reservation *taskstore.ReservationResult `json:"reservation,omitempty"`
	}{History: report}
	if !*preview {
		reservation, err := taskstore.ReserveIDs(*dir, report.IDs, *adopt)
		if err != nil {
			fmt.Fprintln(errOut, "reserve imported IDs:", err)
			return 1
		}
		result.Reservation = &reservation
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write import result (reservations may be applied; repeat safely by union):", err)
		return 1
	}
	return 0
}
