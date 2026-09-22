package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

// runContextValidation validates one standalone context document. It
// deliberately does not inspect a board, register a document, or evaluate
// references; those are separate product operations.
func runContextValidation(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: validate-context <file> --json")
		fmt.Fprintln(out, "Validate one standalone Intent/Batch/Iteration document without board access or writes.")
		return 0
	}
	if len(args) < 3 {
		fmt.Fprintln(errOut, "usage: validate-context <file> --json")
		return 2
	}
	flags := flag.NewFlagSet("validate-context", flag.ContinueOnError)
	flags.SetOutput(errOut)
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || args[1] == "" {
		fmt.Fprintln(errOut, "expected validate-context <file> --json")
		return 2
	}
	raw, err := readValidationInput(args[1], intentdoc.MaxDocumentBytes)
	if err != nil {
		fmt.Fprintln(errOut, "read context:", err)
		return 1
	}
	doc, err := intentdoc.Parse(raw)
	if err != nil {
		fmt.Fprintln(errOut, "validate context:", err)
		return 1
	}
	canonical, err := doc.Canonical()
	if err != nil {
		fmt.Fprintln(errOut, "canonical context:", err)
		return 1
	}
	digest, err := doc.Digest()
	if err != nil {
		fmt.Fprintln(errOut, "context digest:", err)
		return 1
	}
	scope := outputvocab.ScopeIntentBatchDocument
	if doc.Kind() == "iteration" {
		scope = outputvocab.ScopeIterationDocument
	}
	result := struct {
		OutputVersion        int                         `json:"outputVersion"`
		Scope                outputvocab.Scope           `json:"scope"`
		Valid                bool                        `json:"valid"`
		Kind                 string                      `json:"kind"`
		ID                   string                      `json:"id"`
		Revision             uint32                      `json:"revision"`
		Canonical            json.RawMessage             `json:"canonical"`
		Digest               string                      `json:"digest"`
		Registered           bool                        `json:"registered"`
		ReferenceValidation  outputvocab.ValidationState `json:"referenceValidation"`
		EvaluationValidation outputvocab.ValidationState `json:"evaluationValidation"`
	}{outputformat.Version, scope, true, doc.Kind(), doc.ID(), doc.Revision(), canonical, digest, false, outputvocab.NotEvaluated, outputvocab.NotEvaluated}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write context result:", err)
		return 1
	}
	return 0
}
