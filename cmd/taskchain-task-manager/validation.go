package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// Configured validation is deliberately explicit and card-scoped. It must not
// discover repository policies or execute commands embedded in a document.
func runValidation(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage: validate <file> [--config <validation.yaml>] --json")
		fmt.Fprintln(out, "Without --config: syntax only. With --config: work-card rules, not board validation.")
		return 0
	}
	if len(args) < 3 {
		fmt.Fprintln(errOut, "usage: validate <file> [--config <validation.yaml>] --json")
		return 2
	}
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(errOut)
	config := flags.String("config", "", "explicit versioned card validation rules; not board policy")
	asJSON := flags.Bool("json", false, "write JSON")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || args[1] == "" {
		fmt.Fprintln(errOut, "expected validate <file> [--config <validation.yaml>] --json")
		return 2
	}
	configured := false
	flags.Visit(func(f *flag.Flag) { configured = configured || f.Name == "config" })
	if configured && *config == "" {
		fmt.Fprintln(errOut, "--config must name a validation file")
		return 2
	}
	var rules card.ValidationRules
	if configured {
		raw, err := readValidationInput(*config, 64<<10)
		if err == nil {
			rules, err = card.ParseValidationConfig(raw)
		}
		if err != nil {
			fmt.Fprintln(errOut, "validation config:", err)
			return 1
		}
	}
	raw, err := readValidationInput(args[1], 1<<20)
	if err != nil {
		fmt.Fprintln(errOut, "read card:", err)
		return 1
	}
	doc, err := card.Parse(raw)
	if err != nil {
		fmt.Fprintln(errOut, "parse card:", err)
		return 1
	}
	var result any = struct {
		Valid bool `json:"valid"`
	}{true}
	valid := true
	if configured {
		report, err := doc.ValidateCard(args[1], rules)
		if err != nil {
			fmt.Fprintln(errOut, "validate card:", err)
			return 1
		}
		result, valid = report, report.Valid
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write validation result:", err)
		return 1
	}
	if !valid {
		fmt.Fprintln(errOut, "card validation failed; see JSON findings")
		return 1
	}
	return 0
}

func readValidationInput(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular, non-symlink file no larger than %d bytes", path, limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file no larger than %d bytes", path, limit)
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return raw, nil
}
