package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func runLinkEvidence(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: link-evidence <file> --dir <board> --task-id TASK-N --sha256 <64-lowercase-hex> [--receipt-ref <string>] --json")
		return 0
	}
	if len(args) < 2 || args[1] == "" {
		fmt.Fprintln(errOut, "usage: link-evidence <file> --dir <board> --task-id TASK-N --sha256 <64-lowercase-hex> --json")
		return 2
	}
	flags := flag.NewFlagSet("link-evidence", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	taskID := flags.String("task-id", "", "TASK card ID")
	digest := flags.String("sha256", "", "receipt SHA-256")
	ref := flags.String("receipt-ref", "", "inert receipt reference")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" || *taskID == "" || *digest == "" {
		fmt.Fprintln(errOut, "expected link-evidence <file> --dir <board> --task-id TASK-N --sha256 <64-lowercase-hex> --json")
		return 2
	}
	raw, err := readValidationInput(args[1], 256<<10)
	if err != nil {
		fmt.Fprintln(errOut, "read receipt:", err)
		return 1
	}
	result, err := taskstore.LinkEvidence(*dir, *taskID, raw, *digest, *ref)
	if err != nil {
		fmt.Fprintln(errOut, "link evidence:", err)
		return 1
	}
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write evidence result:", err)
		return 1
	}
	return 0
}

func runEvidenceLinks(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: evidence-links --dir <board> --task-id TASK-N --json")
		return 0
	}
	flags := flag.NewFlagSet("evidence-links", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "task board directory")
	taskID := flags.String("task-id", "", "TASK card ID")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" || *taskID == "" {
		fmt.Fprintln(errOut, "expected evidence-links --dir <board> --task-id TASK-N --json")
		return 2
	}
	result, err := taskstore.EvidenceLinks(*dir, *taskID)
	if err != nil {
		fmt.Fprintln(errOut, "evidence links:", err)
		return 1
	}
	if result == nil {
		result = []taskstore.EvidenceLinkResult{}
	}
	if err := outputformat.Encode(out, struct {
		Links []taskstore.EvidenceLinkResult `json:"links"`
	}{result}); err != nil {
		fmt.Fprintln(errOut, "write evidence links:", err)
		return 1
	}
	return 0
}
