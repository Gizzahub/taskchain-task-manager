package taskflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The gate answers one question — can this task board be integrated — by
// running validate, lint, preflight, and checked-binding re-execution in that
// order against one repository. The sequence is the pinned reference's
// (bb970b24) contract: validate first because a card that does not parse makes
// every later answer guesswork; lint before preflight because a card the board
// cannot see is a filing problem, not a queue problem; bindings last because
// re-executing them is the most expensive thing the gate does and the cheaper
// checks should get first refusal.
//
// The verdict vocabulary leaves this process: a consumer's readiness runner
// forwards the summary verbatim, so renaming one is a contract change, not a
// wording change.

// Gate summaries and step names, in the order a board fails them.
const (
	GateSummaryReady       = "task_board_ready"
	GateSummaryValidate    = "task_validate_failed"
	GateSummaryLint        = "task_lint_stray"
	GateSummaryPreflight   = "task_preflight_blocked"
	GateSummaryBinding     = "task_binding_failed"
	GateSummaryUnavailable = "task_gate_unavailable"

	GateStepValidate  = "validate"
	GateStepLint      = "lint"
	GateStepPreflight = "preflight"
	GateStepBindings  = "bindings"

	// GateExitUnavailable separates "the gate could not run" from "the gate
	// ran and the board is not ready". Exit 1 stays reserved for the verdict,
	// because that is the code a caller branches on.
	GateExitUnavailable = 2
)

// GateStep is one check's outcome. Detail carries the step's own error text,
// which is the sentence that says what to fix; the step's full output has
// already gone to stdout in text mode.
type GateStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// GateReport is the whole verdict. Steps is ordered as they ran and stops at
// the first failure — a gate that keeps going after a step failed spends time
// producing findings whose cause is already known.
type GateReport struct {
	Status       string     `json:"status"`
	Summary      string     `json:"summary"`
	ToolVersion  string     `json:"tool_version"`
	ToolRevision string     `json:"tool_revision"`
	FailedStep   string     `json:"failed_step,omitempty"`
	Steps        []GateStep `json:"steps"`
}

// reexecTimeout bounds one checked binding. A checked binding is usually a
// test invocation — this kind of board binds its own test suite — and a cold
// cache compiles the package under test before the test runs, so the probe
// boundary's 5 seconds would report false failures; 30s bounds a stuck
// binding without letting one stall the gate.
const reexecTimeout = 30 * time.Second

// reexecRefusals are the command families the gate will not run even from a
// checked binding, keyed by the command's first word. This is the re-execution
// half of the validate probe boundary, with the polarity deliberately
// opposite: validate executes only an allowlist of read-only predicates,
// while the gate's green verdict asserts "every checked binding was
// re-executed and exited 0" — an allowlist here would silently skip every
// binding outside it. What survives from the probe rules is the discipline:
// a refusal that is loud rather than silent. A refused binding FAILS the step
// instead of being skipped, because green must never rest on a binding the
// gate declined to measure.
var reexecRefusals = map[string]string{
	"curl": "network egress is not evidence about this tree",
	"wget": "network egress is not evidence about this tree",
	"ssh":  "network egress is not evidence about this tree",
	"scp":  "network egress is not evidence about this tree",
	"sftp": "network egress is not evidence about this tree",
	"gh":   "network egress is not evidence about this tree",
	"sudo": "privilege escalation has no place in a board verdict",
	"doas": "privilege escalation has no place in a board verdict",
}

// CheckedBindingFailure is one [x] machine binding that did not exit 0, or
// that the boundary refused to run.
type CheckedBindingFailure struct {
	Path    string
	Line    int
	Command string
	Detail  string
}

func (f CheckedBindingFailure) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.Path, f.Line, f.Command, f.Detail)
}

// ReexecReport is what the bindings step measured: the bindings that did not
// hold, and how many cards the step read. CardsExamined is the step's answer
// to "was anything measured at all" — a board it never saw a card of was not
// judged, whatever the earlier steps said.
type ReexecReport struct {
	Failures      []CheckedBindingFailure
	CardsExamined int
}

// ReexecCheckedBindings runs every checked machine binding under tasksDir, in
// workDir — the repository root the bindings were written against. Human
// checks have no command and are not run (scope: a person, not a shell, is the
// instrument); unchecked checkboxes are claims about the future and are not
// run either. Storage directories are skipped: a frozen record's bindings are
// history, not a claim this gate can falsify.
func ReexecCheckedBindings(ctx context.Context, tasksDir, workDir string) (ReexecReport, error) {
	var report ReexecReport
	err := filepath.WalkDir(tasksDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "evidence", LegacyStorageDir, StorageWriteDir:
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		report.CardsExamined++
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range criterionLines(string(body)) {
			if !line.Checked || line.Command == "" {
				continue
			}
			if reason, refused := reexecRefusalReason(line.Command); refused {
				report.Failures = append(report.Failures, CheckedBindingFailure{
					Path: path, Line: line.Line, Command: line.Command,
					Detail: fmt.Sprintf("refused: %s", reason),
				})
				continue
			}
			detail := runCheckedBinding(ctx, workDir, line.Command)
			if detail != "" {
				report.Failures = append(report.Failures, CheckedBindingFailure{
					Path: path, Line: line.Line, Command: line.Command, Detail: detail})
			}
		}
		return nil
	})
	return report, err
}

// reexecRefusalReason reports whether the boundary refuses this command, and
// why. The first word decides, after a leading `!` is stepped over the way the
// probe classifier does, so a negated refusal cannot walk around the boundary.
func reexecRefusalReason(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if rest, found := strings.CutPrefix(command, "!"); found {
		command = strings.TrimSpace(rest)
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", false
	}
	reason, refused := reexecRefusals[fields[0]]
	return reason, refused
}

// runCheckedBinding executes one binding as shell text — the population's
// bindings are written for a shell, and running an approximation of the
// author's command would answer a question nobody asked. It returns the
// failure detail, empty when the binding exited 0.
func runCheckedBinding(ctx context.Context, workDir, command string) string {
	runCtx, cancel := context.WithTimeout(ctx, reexecTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	detail := strings.TrimSpace(string(out))
	if runCtx.Err() == context.DeadlineExceeded {
		detail = "timed out after 30s"
	}
	if detail == "" {
		detail = err.Error()
	}
	return detail
}

// RequireTasksDirectory refuses a working directory with no tasks directory
// outright, before any stdout: a gate that prints a report about a tree it
// could not find has already lied about having measured something.
func RequireTasksDirectory() error {
	tasksDir, err := ResolveTasksDir()
	if err != nil {
		return err
	}
	info, err := os.Stat(tasksDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("no tasks directory to measure")
	}
	return nil
}

// RunGate runs the checks and stops at the first one that does not pass.
// quiet suppresses each step's own output so that `--json` can put a single
// object on stdout; the step's error text survives in the report either way.
// The steps render into out, which is os.Stdout in text mode.
func RunGate(ctx context.Context, out io.Writer, quiet bool) GateReport {
	report := GateReport{
		Status:       "ready",
		Summary:      GateSummaryReady,
		ToolVersion:  ReferenceStampVersion,
		ToolRevision: ReferenceStampRevision,
	}

	sink := io.Writer(out)
	if quiet {
		sink = io.Discard
	}

	// validate: the whole-board pass. A single invalid card fails the step.
	invalid, err := RenderValidateAll(ctx, sink, ".")
	if err != nil {
		return gateStepUnavailable(report, GateStepValidate, err.Error())
	}
	if invalid > 0 {
		return gateStepFailed(report, GateStepValidate, GateSummaryValidate,
			fmt.Sprintf("%d task(s) failed validation", invalid))
	}
	report.Steps = append(report.Steps, GateStep{Step: GateStepValidate, Status: "pass"})

	// lint: every card somewhere the board looks.
	lint := Lint(".")
	RenderLint(sink, lint)
	if !lint.Clean {
		return gateStepFailed(report, GateStepLint, GateSummaryLint, LintFailureDetail(lint))
	}
	report.Steps = append(report.Steps, GateStep{Step: GateStepLint, Status: "pass"})

	// preflight: a runnable queue, or an honestly closed one. NO_QUEUE is
	// valid for a cleared/reference-only board, so it passes here with its
	// one-line form; BLOCKED and hidden WIP do not pass.
	preflight := PreflightCheck(".", PreflightOptions{})
	RenderPreflightReport(sink, preflight, 0, true)
	if reason, blocked := preflightGateRefusal(preflight); blocked {
		return gateStepFailed(report, GateStepPreflight, GateSummaryPreflight, reason)
	}
	report.Steps = append(report.Steps, GateStep{Step: GateStepPreflight, Status: "pass"})

	return appendGateBindingStep(ctx, report)
}

// preflightGateRefusal names why a preflight verdict fails the gate. Hidden
// WIP outranks the queue verdict: a READY queue with a card that claims
// in-progress from outside doing/ is a run about to duplicate someone's work.
func preflightGateRefusal(report *PreflightReport) (string, bool) {
	if report.Lint != nil && report.Lint.HiddenWIP > 0 {
		return fmt.Sprintf("%d card(s) claim in-progress outside doing/; run `ce task lint` and file them before starting",
			report.Lint.HiddenWIP), true
	}
	if report.Verdict == PreflightBlocked {
		return fmt.Sprintf("queue is not runnable: 0 of %d card(s) can reach done as written", report.Total), true
	}
	return "", false
}

func gateStepFailed(report GateReport, step, summary, detail string) GateReport {
	report.Steps = append(report.Steps, GateStep{Step: step, Status: "fail", Detail: detail})
	report.Status, report.Summary, report.FailedStep = "not-ready", summary, step
	return report
}

func gateStepUnavailable(report GateReport, step, detail string) GateReport {
	report.Steps = append(report.Steps, GateStep{Step: step, Status: "unavailable", Detail: detail})
	report.Status, report.Summary, report.FailedStep = "unavailable", GateSummaryUnavailable, step
	return report
}

// appendGateBindingStep re-executes the board's checked machine bindings.
// "I looked at every card and they all passed" and "I found nothing to look
// at" are different observations: a board whose cards all live in storage was
// not measured, and rc 0 is reserved for a board that was examined and held.
func appendGateBindingStep(ctx context.Context, report GateReport) GateReport {
	tasksDir, err := ResolveTasksDir()
	if err != nil {
		return gateStepUnavailable(report, GateStepBindings, err.Error())
	}
	wd, err := os.Getwd()
	if err != nil {
		return gateStepUnavailable(report, GateStepBindings, err.Error())
	}
	reexec, err := ReexecCheckedBindings(ctx, tasksDir, wd)
	if err != nil {
		return gateStepUnavailable(report, GateStepBindings, err.Error())
	}
	if reexec.CardsExamined == 0 {
		return gateStepUnavailable(report, GateStepBindings,
			fmt.Sprintf("no cards examined under %s: the board was not measured", tasksDir))
	}
	if len(reexec.Failures) == 0 {
		report.Steps = append(report.Steps, GateStep{Step: GateStepBindings, Status: "pass"})
		return report
	}
	return gateStepFailed(report, GateStepBindings, GateSummaryBinding, reexec.Failures[0].String())
}

// RenderGateReport writes the text verdict: one line per step, then the
// summary sentence that names the whole verdict.
func RenderGateReport(w io.Writer, report GateReport) {
	fmt.Fprintln(w)
	for _, step := range report.Steps {
		icon := "✅"
		if step.Status != "pass" {
			icon = "❌"
		}
		fmt.Fprintf(w, "%s %-9s %s\n", icon, step.Step, step.Status)
		if step.Detail != "" {
			fmt.Fprintf(w, "   %s\n", step.Detail)
		}
	}
	switch report.Status {
	case "ready":
		fmt.Fprintf(w, "\nREADY — %s\n", report.Summary)
	case "unavailable":
		fmt.Fprintf(w, "\nUNAVAILABLE — %s (the gate could not judge; fix the cause above)\n", report.Summary)
	default:
		fmt.Fprintf(w, "\nNOT READY — %s (%s)\n", report.Summary, report.FailedStep)
	}
}

// RenderGateJSON writes the machine-readable verdict: one object on stdout.
func RenderGateJSON(w io.Writer, report GateReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}
