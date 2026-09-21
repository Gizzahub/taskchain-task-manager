package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

func runPolicyValidation(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: validate-policy <file> --json")
		fmt.Fprintln(out, "Validate one policy document without activating it or inspecting a board.")
		return 0
	}
	if len(args) < 3 {
		fmt.Fprintln(errOut, "usage: validate-policy <file> --json")
		return 2
	}
	flags := flag.NewFlagSet("validate-policy", flag.ContinueOnError)
	flags.SetOutput(errOut)
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || args[1] == "" {
		fmt.Fprintln(errOut, "expected validate-policy <file> --json")
		return 2
	}
	raw, err := readValidationInput(args[1], 64<<10)
	if err != nil {
		fmt.Fprintln(errOut, "read policy:", err)
		return 1
	}
	policy, err := boardpolicy.Parse(raw)
	if err != nil {
		fmt.Fprintln(errOut, "validate policy:", err)
		return 1
	}
	canonical, err := policy.Canonical()
	if err != nil {
		fmt.Fprintln(errOut, "canonical policy:", err)
		return 1
	}
	digest, err := policy.Digest()
	if err != nil {
		fmt.Fprintln(errOut, "policy digest:", err)
		return 1
	}
	result := struct {
		SchemaVersion   int                         `json:"schemaVersion"`
		Scope           outputvocab.Scope           `json:"scope"`
		Valid           bool                        `json:"valid"`
		Activated       bool                        `json:"activated"`
		BoardValidation outputvocab.ValidationState `json:"boardValidation"`
		Digest          string                      `json:"digest"`
		Canonical       json.RawMessage             `json:"canonical"`
	}{1, outputvocab.ScopePolicyDocument, true, false, outputvocab.NotEvaluated, digest, canonical}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write policy result:", err)
		return 1
	}
	return 0
}
