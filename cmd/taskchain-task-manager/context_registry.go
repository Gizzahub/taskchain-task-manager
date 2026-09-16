package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func runRegisterContext(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: register-context <file> --dir <board> --json")
		fmt.Fprintln(out, "Register one immutable Intent/Batch document in a board-local registry.")
		return 0
	}
	if len(args) < 3 || args[1] == "" {
		fmt.Fprintln(errOut, "usage: register-context <file> --dir <board> --json")
		return 2
	}
	flags := flag.NewFlagSet("register-context", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" {
		fmt.Fprintln(errOut, "expected register-context <file> --dir <board> --json")
		return 2
	}
	raw, err := readValidationInput(args[1], intentdoc.MaxDocumentBytes)
	if err != nil {
		fmt.Fprintln(errOut, "read context:", err)
		return 1
	}
	result, err := taskstore.RegisterContext(*dir, raw)
	if err != nil {
		fmt.Fprintln(errOut, "register context:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write context result (it may already be registered; retry the same file and board):", err)
		return 1
	}
	return 0
}

func runShowContext(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: show-context --dir <board> --kind intent|batch --id ID --revision N --json")
		fmt.Fprintln(out, "Show one immutable board-local Intent/Batch registration.")
		return 0
	}
	flags := flag.NewFlagSet("show-context", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	kind := flags.String("kind", "", "intent or batch")
	id := flags.String("id", "", "context ID")
	revision := flags.String("revision", "", "positive uint32 revision")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" || *kind == "" || *id == "" || *revision == "" {
		fmt.Fprintln(errOut, "expected show-context --dir <board> --kind intent|batch --id ID --revision N --json")
		return 2
	}
	if *kind != "intent" && *kind != "batch" {
		fmt.Fprintln(errOut, "show-context: --kind must be intent or batch")
		return 2
	}
	rev, err := parseContextRevision(*revision)
	if err != nil {
		fmt.Fprintln(errOut, "show-context: revision:", err)
		return 2
	}
	result, err := taskstore.ShowContext(*dir, *kind, *id, rev)
	if err != nil {
		fmt.Fprintln(errOut, "show context:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write context result:", err)
		return 1
	}
	return 0
}

func parseContextRevision(raw string) (uint32, error) {
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 || n > math.MaxUint32 {
		return 0, errors.New("must be a positive uint32")
	}
	return uint32(n), nil
}
