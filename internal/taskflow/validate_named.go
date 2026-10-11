package taskflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"gopkg.in/yaml.v3"
)

// CheckConfigYAML fails closed when ce-tasks.yaml exists and is not YAML, or
// when task-board.zone-skills is present but is not a strict map of nonempty
// strings onto zones named by card-dialect.zones. A missing file is not an
// error. Other top-level blocks, such as task-gate and task-maintenance, stay
// accepted.
func CheckConfigYAML(root string) error {
	path := filepath.Join(root, "ce-tasks.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read ce-tasks.yaml: %w", err)
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode ce-tasks.yaml: %w", err)
	}
	if _, _, err := boardpolicy.ParseTaskBoardConfig(raw); err != nil {
		return fmt.Errorf("decode ce-tasks.yaml: %w", err)
	}
	return nil
}

// cardCheck is an optional deviation from the whole-board card checks.
// The zero value is what validate --all uses.
type cardCheck struct {
	// skipVacuousProbe keeps reportVacuousCriteria from running verify
	// commands. Named and staged validation execute a verify command only
	// through AdmitCriteria, and only against the selected probe directory.
	skipVacuousProbe bool
}

// CriterionTree is the probe root named validation admits criteria against.
// Selected is true for a --base snapshot. The work tree leaves it false.
// Refusal is set when the commit resolved but its content is one the spec
// calls unavailable (a submodule, or a symlink the snapshot refuses). Probes
// do not run, and the command does not fail before admission.
type CriterionTree struct {
	Description string
	ProbeDir    string
	Selected    bool
	Refusal     string
	cleanup     string
}

// Cleanup removes a --base snapshot. A removal failure is a diagnostic.
func (t CriterionTree) Cleanup(errOut io.Writer) {
	if t.cleanup == "" {
		return
	}
	if err := os.RemoveAll(t.cleanup); err != nil && errOut != nil {
		fmt.Fprintf(errOut, "warning: remove criterion snapshot: %v\n", err)
	}
}

// ValidateNamed validates each named card, prints admission, and returns how
// many cards failed plus the first card's error count (for the single-file
// refusal). already-passing errors follow the probe tree, not a second probe
// of the work tree.
func ValidateNamed(ctx context.Context, w io.Writer, root, tasksDir string, files []string, tree CriterionTree) (invalid, firstErrors int, err error) {
	index := buildCorpusIndex(root)
	var citations Citations
	total := Admission{Tree: tree.Description}
	for i, file := range files {
		card, readErr := readNamedCard(root, tasksDir, file)
		if readErr != nil {
			return 0, 0, readErr
		}
		var result ValidationResult
		if isDecisionDoc(card.TasksRel) && !isNonCardFile(filepath.Base(card.TasksRel)) {
			result = validateDecisionDocument(ctx, root, tasksDir, card)
		} else {
			result = validateCard(ctx, root, tasksDir, card, index, &citations, cardCheck{skipVacuousProbe: true})
		}
		admission := AdmitCriteria(ctx, tree.ProbeDir, card.Zone, tree.Description, tree.Selected, tree.Refusal, card.Criteria())
		applyAdmission(&result, admission)
		writeNamedResult(w, result, admission)
		fmt.Fprintln(w)
		if i == 0 {
			firstErrors = len(result.Errors)
		}
		if !result.Valid() {
			invalid++
		}
		total = total.merge(admission)
	}
	if len(files) > 1 {
		fmt.Fprintln(w, "---")
		fmt.Fprintf(w, "Summary: %d valid, %d invalid (total: %d)\n", len(files)-invalid, invalid, len(files))
		fmt.Fprintln(w, total.Format())
		if total.Tree != "" {
			fmt.Fprintf(w, "  Criterion tree: %s\n", total.Tree)
		}
	}
	return invalid, firstErrors, nil
}

func readNamedCard(root, tasksDir, file string) (*Card, error) {
	full := file
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, file)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read task file %s: %w", file, err)
	}
	rel, relErr := filepath.Rel(filepath.Join(root, tasksDir), full)
	tasksRel := filepath.ToSlash(filepath.Clean(file))
	if relErr == nil && rel != "" && !strings.HasPrefix(rel, "..") {
		tasksRel = filepath.ToSlash(rel)
	}
	return ParseCard(tasksRel, raw), nil
}

// applyAdmission installs the admission probe's errors and warnings. A
// vacuous finding recorded against the work tree, if one is still present,
// is dropped: named validation does not probe before admission, and a
// --base message must name the snapshot.
func applyAdmission(result *ValidationResult, admission Admission) {
	kept := make([]Finding, 0, len(result.Errors)+len(admission.Errors))
	for _, finding := range result.Errors {
		if finding.Field == "verify" && strings.Contains(finding.Message, "Criterion already passes") {
			continue
		}
		kept = append(kept, finding)
	}
	result.Errors = append(kept, admission.Errors...)
	result.Warnings = append(result.Warnings, admission.Warnings...)
}

func writeNamedResult(w io.Writer, result ValidationResult, admission Admission) {
	fmt.Fprintf(w, "Validating: %s\n", result.Path)
	fmt.Fprintln(w, admission.Format())
	if admission.Tree != "" {
		fmt.Fprintf(w, "  Criterion tree: %s\n", admission.Tree)
	}
	for _, finding := range result.Errors {
		fmt.Fprintf(w, "  ❌ %s\n", finding.Message)
	}
	for _, finding := range result.Warnings {
		fmt.Fprintf(w, "  ⚠️  %s\n", finding.Message)
	}
	switch {
	case len(result.Errors) == 0 && len(result.Warnings) == 0:
		fmt.Fprintln(w, "  ✅ Valid (no errors or warnings)")
	case len(result.Errors) == 0:
		fmt.Fprintf(w, "  ⚠️  Valid with %d warning(s)\n", len(result.Warnings))
	default:
		fmt.Fprintf(w, "  ❌ Invalid: %d error(s), %d warning(s)\n", len(result.Errors), len(result.Warnings))
	}
}

// CorpusEmpty reports whether the board, storage included, holds no card.
func CorpusEmpty(root, tasksDir string) bool {
	cards, err := FindCardsIn(root, tasksDir, false)
	return err != nil || len(cards) == 0
}
