package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func runCreateBundle(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: create-bundle <file> --dir <board> [--adopt] [--resume] --json")
		fmt.Fprintln(out, "Publish one immutable task bundle; --adopt upgrades the bundle journal protocol after all writers are upgraded.")
		fmt.Fprintln(out, "--resume is only for the same recorded request.")
		return 0
	}
	if len(args) < 3 || args[1] == "" {
		fmt.Fprintln(errOut, "usage: create-bundle <file> --dir <board> [--adopt] [--resume] --json")
		return 2
	}
	flags := flag.NewFlagSet("create-bundle", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	adopt := flags.Bool("adopt", false, "upgrade bundle journal protocol after all writers are upgraded")
	resume := flags.Bool("resume", false, "resume the same recorded request")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *dir == "" || !*asJSON {
		fmt.Fprintln(errOut, "expected create-bundle <file> --dir <board> [--adopt] [--resume] --json")
		return 2
	}
	raw, err := readValidationInput(args[1], intentdoc.MaxDocumentBytes)
	if err != nil {
		fmt.Fprintln(errOut, "read bundle:", err)
		return 1
	}
	result, err := taskstore.PublishBundle(*dir, raw, taskstore.BundleOptions{Adopt: *adopt, Resume: *resume})
	if err != nil {
		fmt.Fprintln(errOut, "publish bundle:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write bundle result (transaction may already be completed; retry the same request):", err)
		return 1
	}
	return 0
}
