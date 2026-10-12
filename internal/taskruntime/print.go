package taskruntime

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteResponse prints one run response. jsonOutput selects the envelope.
// listMode is the run-list human surface: live and undecided records each
// get a line, and terminal records fold into a count.
func WriteResponse(w io.Writer, result Response, jsonOutput, listMode bool) error {
	if jsonOutput {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}
	if _, err := fmt.Fprintf(w, "%s", result.Status); err != nil {
		return err
	}
	if result.Task != "" {
		if _, err := fmt.Fprintf(w, " %s", result.Task); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, ": %s\n", result.Reason); err != nil {
		return err
	}
	if result.Execution != nil {
		if _, err := fmt.Fprintf(w, "purpose: %s\nbranch: %s\nworktree: %s\nowner: %s\n", EffectivePurpose(result.Execution.Purpose), result.Execution.Branch, result.Execution.Worktree, result.Execution.Owner.String()); err != nil {
			return err
		}
	}
	if listMode {
		if err := writeExecutionStates(w, result.States); err != nil {
			return err
		}
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	if result.NextAction != "" {
		if _, err := fmt.Fprintf(w, "next: %s\n", result.NextAction); err != nil {
			return err
		}
	}
	return nil
}

func writeExecutionStates(w io.Writer, states []DerivedState) error {
	if len(states) == 0 {
		_, err := fmt.Fprintln(w, "executions: none")
		return err
	}
	settled := map[string]int{}
	for _, state := range states {
		if TerminalStatus(state.Status) {
			settled[state.Status]++
			continue
		}
		if _, err := fmt.Fprintf(w, "%s %s: purpose=%s branch=%s worktree=%s", state.Status, state.Execution.Task, EffectivePurpose(state.Execution.Purpose), state.Execution.Branch, state.Execution.Worktree); err != nil {
			return err
		}
		if state.Status != StatusActive && state.Reason != "" {
			if _, err := fmt.Fprintf(w, " reason=%s", state.Reason); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if len(settled) == 0 {
		return nil
	}
	statuses := make([]string, 0, len(settled))
	for status := range settled {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	counts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		counts = append(counts, fmt.Sprintf("%s=%d", status, settled[status]))
	}
	_, err := fmt.Fprintf(w, "settled: %s\n", strings.Join(counts, " "))
	return err
}
