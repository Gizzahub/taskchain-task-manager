package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"io"
)

func runTransition(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "tasks", "task board directory")
	id := flags.String("id", "", "canonical task ID")
	owner := flags.String("owner", "", "claim owner")
	token := flags.String("token", "", "held claim token")
	requestID := flags.String("request-id", "", "unique transition retry identifier (32 lowercase hex)")
	from := flags.String("from", "", "expected source workflow zone")
	to := flags.String("to", "", "target workflow zone")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" || *id == "" || *owner == "" || *token == "" || *requestID == "" || *from == "" || *to == "" {
		fmt.Fprintln(errOut, "expected --dir <board> --id TASK-N --owner <owner> --token <claim token> --request-id <32 lowercase hex> --from <zone> --to <zone> --json")
		return 2
	}
	req := taskstore.TransitionRequest{ID: *id, Owner: *owner, Token: *token, RequestID: *requestID, From: *from, To: *to}
	var result taskstore.TransitionResult
	var err error
	if args[0] == "recover" {
		result, err = taskstore.Recover(*dir, req)
	} else {
		result, err = taskstore.Transition(*dir, req)
	}
	if err != nil {
		fmt.Fprintln(errOut, args[0]+":", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write result (retry with exactly the same request-id and arguments):", err)
		return 1
	}
	return 0
}
