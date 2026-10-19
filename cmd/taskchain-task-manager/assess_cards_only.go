package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Gizzahub/taskchain-task-manager/internal/candidatediagnostic"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
)

const assessCardsOnlyUsage = "usage: taskchain-task-manager assess-cards-only --repo ROOT --board PATH --candidate-policy FILE --source worktree|index|ref [--ref REV] --json"

// assessCardsOnly is a seam for command tests. The diagnostic package owns all
// candidate reads, capture, and assessment; this command only owns its wire.
var assessCardsOnly = candidatediagnostic.Assess

func runAssessCardsOnly(args []string, out, errOut io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runAssessCardsOnlyWithContext(ctx, args, out, errOut)
}

func runAssessCardsOnlyWithContext(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 2 && args[1] == "--help" {
		if _, err := fmt.Fprintln(shortWriteErrorWriter{Writer: out}, assessCardsOnlyUsage); err != nil {
			fmt.Fprintln(errOut, "assess-cards-only: write help failed")
			return 2
		}
		return 0
	}

	input, ok := parseAssessCardsOnlyInput(args)
	if !ok {
		fmt.Fprintln(errOut, assessCardsOnlyUsage)
		return 2
	}
	if err := candidatediagnostic.ValidateInput(input); err != nil {
		// ValidateInput may inspect untrusted paths. Its diagnostics are not a
		// public reflection surface for command-line input.
		fmt.Fprintln(errOut, "assess-cards-only: invalid input")
		return 2
	}

	result := assessCardsOnly(ctx, input)
	if err := outputformat.Encode(shortWriteErrorWriter{Writer: out}, result); err != nil {
		fmt.Fprintln(errOut, "assess-cards-only: write result failed")
		return 2
	}
	return result.ExitCode()
}

func parseAssessCardsOnlyInput(args []string) (candidatediagnostic.Input, bool) {
	if len(args) == 0 || args[0] != "assess-cards-only" {
		return candidatediagnostic.Input{}, false
	}
	var input candidatediagnostic.Input
	seen := make(map[string]bool, 6)
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" {
			if seen[arg] {
				return candidatediagnostic.Input{}, false
			}
			seen[arg] = true
			continue
		}
		if arg != "--repo" && arg != "--board" && arg != "--candidate-policy" && arg != "--source" && arg != "--ref" {
			return candidatediagnostic.Input{}, false
		}
		if seen[arg] || i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "--") {
			return candidatediagnostic.Input{}, false
		}
		seen[arg] = true
		i++
		switch arg {
		case "--repo":
			input.Repo = args[i]
		case "--board":
			input.Board = args[i]
		case "--candidate-policy":
			input.CandidatePolicy = args[i]
		case "--source":
			input.Source = args[i]
		case "--ref":
			input.Ref = args[i]
		}
	}
	if !seen["--repo"] || !seen["--board"] || !seen["--candidate-policy"] || !seen["--source"] || !seen["--json"] {
		return candidatediagnostic.Input{}, false
	}
	if input.Source != "worktree" && input.Source != "index" && input.Source != "ref" {
		return candidatediagnostic.Input{}, false
	}
	if (input.Source == "ref") != seen["--ref"] {
		return candidatediagnostic.Input{}, false
	}
	return input, true
}

// shortWriteErrorWriter supplies io.Writer's required all-or-error contract to
// outputformat.Encode without changing its established semantics for commands
// that already use it directly.
type shortWriteErrorWriter struct{ io.Writer }

func (w shortWriteErrorWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, err
}
