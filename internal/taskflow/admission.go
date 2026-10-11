package taskflow

import (
	"context"
	"fmt"
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
}

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
// Cards outside the todo zone have nothing to admit.
func AdmitCriteria(ctx context.Context, probeDir, zone, tree string, criteria []Criterion) Admission {
	report := Admission{Tree: tree}
	if zone != StatusPending.Dir() {
		return report
	}
	for _, criterion := range criteria {
		if criterion.Checked || strings.TrimSpace(criterion.Command) == "" {
			continue
		}
		report.Eligible++
		command := criterion.Command
		if strings.ContainsAny(command, ";|") {
			report.UnexecutedCompound++
			continue
		}
		if err := classifyProbe(command); err != nil {
			report.Unsupported++
			continue
		}
		passed, executable, err := execProbe(ctx, probeDir, command)
		if err != nil || !executable {
			report.Unavailable++
			continue
		}
		report.Examined++
		if passed {
			report.AlreadyPassing++
			report.Passing = append(report.Passing, criterion.Text)
		}
	}
	return report
}
