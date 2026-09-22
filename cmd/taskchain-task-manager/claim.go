package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"io"
)

func runClaim(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "tasks", "task board directory")
	id := flags.String("id", "", "canonical task ID")
	owner := flags.String("owner", "", "explicit local owner identity (not authentication)")
	token := flags.String("token", "", "unique 32 lowercase hex retry identifier")
	asJSON := flags.Bool("json", false, "write JSON")
	var resume bool
	if args[0] == "claim" {
		flags.BoolVar(&resume, "resume", false, "explicitly reserve an unclaimed non-todo workflow card")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" || *id == "" || *owner == "" || *token == "" {
		fmt.Fprintln(errOut, "expected --dir <board> --id TASK-N --owner <owner> --token <32 lowercase hex> --json")
		return 2
	}
	req := taskstore.ClaimRequest{ID: *id, Owner: *owner, Token: *token}
	var result taskstore.ClaimRecord
	var err error
	if args[0] == "claim" && resume {
		result, err = taskstore.ClaimResume(*dir, req)
	} else if args[0] == "claim" {
		result, err = taskstore.Claim(*dir, req)
	} else {
		result, err = taskstore.Release(*dir, req)
	}
	if err != nil {
		fmt.Fprintln(errOut, args[0]+":", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write result (retry the same id, owner and token):", err)
		return 1
	}
	return 0
}
