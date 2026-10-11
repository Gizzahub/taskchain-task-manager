package taskflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Admission is how much of a card's unchecked command bindings a probe could
// run. Incomplete admission is not itself a validation failure.
type Admission struct {
	Tree               string
	Eligible           int
	Examined           int
	AlreadyPassing     int
	UnexecutedCompound int
	Unsupported        int
	Unavailable        int
	Passing            []string
	Errors             []Finding
	Warnings           []Finding
}

const (
	admissionShellWarning       = "Criterion was not executed because its verify binding requires shell syntax: "
	admissionUnsupportedWarning = "Criterion was not executed because its verify command is unsupported: "
	admissionOperandWarning     = "Criterion was not executed because its operands are not contained by the selected tree: "
	// admissionSnapshotWarning is the criterion_admission warning for a
	// resolved --base whose content the spec calls unavailable: a submodule,
	// or an absolute, empty, cyclic, or escaping symlink.
	admissionSnapshotWarning = "Criterion was not executed because the selected tree is unavailable: "
)

// Status is the compact completeness word.
func (r Admission) Status() string {
	if r.Eligible == 0 {
		return "not-applicable"
	}
	if r.Unavailable > 0 {
		return "unavailable"
	}
	if r.UnexecutedCompound > 0 || r.Unsupported > 0 || r.Examined < r.Eligible {
		return "incomplete"
	}
	if r.AlreadyPassing > 0 {
		return "already-passing"
	}
	return "complete"
}

// Format is the indented admission line named validation prints.
func (r Admission) Format() string {
	return fmt.Sprintf("  Criterion admission: %s; %d eligible, %d examined, %d already passing, %d compound unexecuted, %d unsupported, %d unavailable",
		r.Status(), r.Eligible, r.Examined, r.AlreadyPassing, r.UnexecutedCompound, r.Unsupported, r.Unavailable)
}

func (r Admission) merge(one Admission) Admission {
	if one.Tree != "" {
		r.Tree = one.Tree
	}
	r.Eligible += one.Eligible
	r.Examined += one.Examined
	r.AlreadyPassing += one.AlreadyPassing
	r.UnexecutedCompound += one.UnexecutedCompound
	r.Unsupported += one.Unsupported
	r.Unavailable += one.Unavailable
	return r
}

// AdmitCriteria classifies unchecked verify commands against probeDir.
// Cards outside the todo zone have nothing to admit. selected is a --base
// snapshot: operands that leave that tree are unavailable, and a criterion
// that already passes names the snapshot. refusal is a snapshot the spec
// calls unavailable; no verify command runs in that case.
func AdmitCriteria(ctx context.Context, probeDir, zone, tree string, selected bool, refusal string, criteria []Criterion) Admission {
	report := Admission{Tree: tree}
	if zone != StatusPending.Dir() {
		return report
	}
	for _, criterion := range criteria {
		if criterion.Checked || strings.TrimSpace(criterion.Command) == "" {
			continue
		}
		report.Eligible++
		text := strings.TrimSpace(criterion.Text)
		if refusal != "" {
			report.Unavailable++
			report.Warnings = append(report.Warnings, Finding{"criterion_admission", admissionSnapshotWarning + refusal + ": " + text})
			continue
		}
		if compoundProbe(criterion.Command) {
			report.UnexecutedCompound++
			report.Warnings = append(report.Warnings, Finding{"criterion_admission", admissionShellWarning + text})
			continue
		}
		argv, negated, err := parseAdmittedProbe(criterion.Command)
		if err != nil {
			report.Unsupported++
			report.Warnings = append(report.Warnings, Finding{"criterion_admission", admissionUnsupportedWarning + text})
			continue
		}
		if !selected {
			if _, err := os.Lstat(filepath.Join(probeDir, ".git")); err != nil {
				report.Unavailable++
				continue
			}
		} else if !snapshotProbeAllowed(probeDir, argv) {
			report.Unavailable++
			report.Warnings = append(report.Warnings, Finding{"criterion_admission", admissionOperandWarning + text})
			continue
		}
		if selected && len(argv) > 0 && argv[0] == "rg" {
			argv = append([]string{"rg", "--no-config"}, argv[1:]...)
		}
		exitZero, err := runAdmittedProbe(ctx, probeDir, argv, selected)
		if err != nil {
			report.Unavailable++
			continue
		}
		report.Examined++
		if negated {
			exitZero = !exitZero
		}
		if !exitZero {
			continue
		}
		report.AlreadyPassing++
		report.Passing = append(report.Passing, criterion.Text)
		report.Errors = append(report.Errors, Finding{"vacuous_criterion", vacuousMessage(selected, tree, text)})
	}
	return report
}

func vacuousMessage(selected bool, tree, text string) string {
	if selected {
		return fmt.Sprintf("Criterion already passes on the selected tree (%s), so completing this card would prove nothing: %s", tree, text)
	}
	return fmt.Sprintf("Criterion already passes on the current tree, so completing this card would prove nothing: %s", text)
}
