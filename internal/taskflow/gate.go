package taskflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// The gate re-executes what a card claims was verified: a done path is not
// proof of verified implementation, so the CHECKED machine bindings of every
// card run again, and the verdict names the step that refused.

// GateToolVersion and GateToolRevision are the pinned reference identity the
// parity fixtures measured; the gate's JSON cites the reference it reproduces.
const (
	GateToolVersion  = "v0.8.4-400-gbb970b24"
	GateToolRevision = "bb970b24adfc29f75e7555cb3cc45716d6973f3b"
)

// Gate step statuses.
const (
	gateStepPass        = "pass"
	gateStepFail        = "fail"
	gateStepUnavailable = "unavailable"
)

// GateStep is one measured step of the gate.
type GateStep struct {
	Name   string
	Status string
	Detail []string
}

// GateOutcome is the gate's verdict: the four steps as measured, the step that
// refused, the summary code a caller gates on, and the exit code that matches.
type GateOutcome struct {
	Steps      []GateStep
	FailedStep string
	Summary    string
	Verdict    string // READY, NOT READY, or UNAVAILABLE
	Note       string
	ExitCode   int
}

// gateFailureSummaries maps a failed step to the code its refusal reports.
var gateFailureSummaries = map[string]string{
	"validate":  "task_validation_failed",
	"lint":      "task_lint_failed",
	"preflight": "task_preflight_failed",
	"bindings":  "task_binding_failed",
}

// RunGate measures the four gate steps in order and renders them. Text mode
// streams each step's section as it is measured, then the step table and
// verdict; JSON mode prints only the JSON payload.
func RunGate(ctx context.Context, w io.Writer, root string, asJSON bool) (*GateOutcome, error) {
	invalid, err := renderValidateForGate(ctx, w, root, asJSON)
	if err != nil {
		return nil, err
	}
	validateStep := GateStep{Name: "validate", Status: gateStepPass}
	if invalid > 0 {
		validateStep.Status = gateStepFail
		validateStep.Detail = []string{fmt.Sprintf("%d task(s) failed validation", invalid)}
	}

	lint, err := CollectLint(root)
	if err != nil {
		return nil, err
	}
	if !asJSON {
		RenderLintText(w, lint)
	}
	lintStep := GateStep{Name: "lint", Status: gateStepPass}
	if !lint.Clean() {
		lintStep.Status = gateStepFail
		lintStep.Detail = []string{lint.CensusLine()}
	}

	preflight, err := CollectPreflight(root)
	if err != nil {
		return nil, err
	}
	if !asJSON {
		RenderPreflightText(w, preflight)
	}
	preflightStep := GateStep{Name: "preflight", Status: gateStepPass}
	if preflight.Verdict == "NOT_READY" {
		preflightStep.Status = gateStepFail
		preflightStep.Detail = []string{fmt.Sprintf("queue not ready: %d unrunnable, %d blocking",
			preflight.Unrunnable, len(preflight.Blocking))}
	}

	bindingsStep, err := gateBindings(ctx, root)
	if err != nil {
		return nil, err
	}
	steps := []GateStep{validateStep, lintStep, preflightStep, bindingsStep}
	outcome := gateVerdict(steps)
	if asJSON {
		if err := renderGateJSON(w, outcome); err != nil {
			return nil, err
		}
	} else {
		renderGateSections(w, outcome)
	}
	return outcome, nil
}

// renderValidateForGate streams the whole-board validate section and returns
// the invalid count the step grades on.
func renderValidateForGate(ctx context.Context, w io.Writer, root string, asJSON bool) (int, error) {
	if asJSON {
		results, _, err := ValidateAll(ctx, root)
		if err != nil {
			return 0, err
		}
		invalid := 0
		for _, result := range results {
			if len(result.Errors) > 0 {
				invalid++
			}
		}
		return invalid, nil
	}
	return RenderValidateAll(ctx, w, root)
}

// gateBindings re-executes the checked machine bindings of every card. An
// empty board cannot be measured; a binding the probe boundary refuses is not
// measurable here and is left to validate's citation census.
func gateBindings(ctx context.Context, root string) (GateStep, error) {
	step := GateStep{Name: "bindings", Status: gateStepPass}
	probeDir, err := probeRoot(root)
	if err != nil {
		step.Status = gateStepUnavailable
		step.Detail = []string{"no git repository: the board was not measured"}
		return step, nil
	}
	cards, err := FindCards(root, true)
	if err != nil {
		return step, err
	}
	if len(cards) == 0 {
		step.Status = gateStepUnavailable
		step.Detail = []string{"no cards examined under tasks: the board was not measured"}
		return step, nil
	}
	for _, card := range cards {
		for _, criterion := range card.Criteria() {
			if !criterion.Checked || criterion.Command == "" {
				continue
			}
			passed, status, executable, err := execProbeStatus(ctx, probeDir, criterion.Command)
			if err != nil || !executable {
				continue
			}
			if !passed {
				step.Status = gateStepFail
				step.Detail = append(step.Detail, fmt.Sprintf("%s:%d: %s: %s",
					card.RepoRel(), card.CriterionFileLine(criterion), criterion.Command, status))
			}
		}
	}
	return step, nil
}

// gateVerdict folds the measured steps into the outcome. An unavailable step
// outranks a failing one: a gate that could not judge never reports a verdict
// it did not measure.
func gateVerdict(steps []GateStep) *GateOutcome {
	outcome := &GateOutcome{Steps: steps}
	for _, step := range steps {
		if step.Status == gateStepUnavailable {
			outcome.Verdict = "UNAVAILABLE"
			outcome.Summary = "task_gate_unavailable"
			outcome.Note = "the gate could not judge; fix the cause above"
			outcome.FailedStep = step.Name
			outcome.ExitCode = 2
			return outcome
		}
	}
	for _, step := range steps {
		if step.Status == gateStepFail {
			outcome.Verdict = "NOT READY"
			outcome.Summary = gateFailureSummaries[step.Name]
			outcome.Note = step.Name
			outcome.FailedStep = step.Name
			outcome.ExitCode = 1
			return outcome
		}
	}
	outcome.Verdict = "READY"
	outcome.Note = "all steps pass"
	outcome.ExitCode = 0
	return outcome
}

// renderGateSections writes the step table and verdict that close a text gate.
func renderGateSections(w io.Writer, outcome *GateOutcome) {
	fmt.Fprintln(w)
	for _, step := range outcome.Steps {
		icon := "✅"
		if step.Status != gateStepPass {
			icon = "❌"
		}
		fmt.Fprintf(w, "%s %-10s%s\n", icon, step.Name, step.Status)
		for _, detail := range step.Detail {
			fmt.Fprintf(w, "   %s\n", detail)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s — %s (%s)\n", outcome.Verdict, gateSummaryText(outcome), outcome.Note)
}

// gateSummaryText is the summary code both renderings cite; a ready gate has
// no refusal to report, so it names the code the JSON mode carries instead of
// an empty word.
func gateSummaryText(outcome *GateOutcome) string {
	if outcome.Summary == "" {
		return "task_gate_ready"
	}
	return outcome.Summary
}

// gateStepJSON is one step of the machine-readable gate.
type gateStepJSON struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// gateJSON is the machine-readable gate verdict. Field order is the byte
// contract the fixtures pin.
type gateJSON struct {
	Status       string         `json:"status"`
	Summary      string         `json:"summary"`
	ToolVersion  string         `json:"tool_version"`
	ToolRevision string         `json:"tool_revision"`
	FailedStep   string         `json:"failed_step,omitempty"`
	Steps        []gateStepJSON `json:"steps"`
}

func renderGateJSON(w io.Writer, outcome *GateOutcome) error {
	status := strings.ToLower(strings.ReplaceAll(outcome.Verdict, " ", "-"))
	payload := gateJSON{
		Status:       status,
		Summary:      gateSummaryText(outcome),
		ToolVersion:  GateToolVersion,
		ToolRevision: GateToolRevision,
		FailedStep:   outcome.FailedStep,
		Steps:        make([]gateStepJSON, 0, len(outcome.Steps)),
	}
	for _, step := range outcome.Steps {
		entry := gateStepJSON{Step: step.Name, Status: step.Status}
		if len(step.Detail) > 0 {
			entry.Detail = step.Detail[0]
		}
		payload.Steps = append(payload.Steps, entry)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}
