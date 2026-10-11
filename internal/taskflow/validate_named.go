package taskflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CheckConfigYAML fails closed when ce-tasks.yaml exists and is not YAML.
// A missing file is the default dialect and is not an error. The product has
// no card-dialect schema, so this is the malformed-file check only.
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
	return nil
}

// CriterionTree is the probe root named validation admits criteria against.
type CriterionTree struct {
	Description string
	ProbeDir    string
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

// SelectCriterionTree resolves --base to a tracked snapshot, or describes the
// mutable work tree. base empty selects the work tree. The snapshot is only
// for criterion probes; cards are still read from root.
func SelectCriterionTree(ctx context.Context, root, base string) (CriterionTree, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if strings.TrimSpace(base) == "" {
		desc := fmt.Sprintf("current tree %s (HEAD unavailable; mutable working tree)", abs)
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		head, err := gitOutput(probeCtx, abs, "--no-replace-objects", "--no-lazy-fetch", "rev-parse", "--verify", "HEAD^{commit}")
		if err == nil && head != "" {
			desc = fmt.Sprintf("current tree %s (HEAD %s; mutable working tree)", abs, strings.TrimSpace(head))
		}
		return CriterionTree{Description: desc, ProbeDir: abs}, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sha, err := gitOutput(probeCtx, abs, "rev-parse", "--verify", strings.TrimSpace(base)+"^{commit}")
	if err != nil {
		return CriterionTree{}, fmt.Errorf("criterion base unavailable: %w", err)
	}
	sha = strings.TrimSpace(sha)
	dir := filepath.Join(abs, "build", "tmp", "criterion-snapshots", sha)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CriterionTree{}, fmt.Errorf("criterion base unavailable: %w", err)
	}
	archive := exec.CommandContext(probeCtx, "git", "-C", abs, "archive", "--format=tar", sha)
	tar := exec.CommandContext(probeCtx, "tar", "-x", "-C", dir)
	tar.Stdin, err = archive.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return CriterionTree{}, fmt.Errorf("criterion base unavailable: %w", err)
	}
	if err := tar.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return CriterionTree{}, fmt.Errorf("criterion base unavailable: %w", err)
	}
	if err := archive.Run(); err != nil {
		_ = tar.Wait()
		_ = os.RemoveAll(dir)
		return CriterionTree{}, fmt.Errorf("criterion base unavailable: %w", err)
	}
	if err := tar.Wait(); err != nil {
		_ = os.RemoveAll(dir)
		return CriterionTree{}, fmt.Errorf("criterion base unavailable: %w", err)
	}
	return CriterionTree{
		Description: fmt.Sprintf("commit %s (tracked snapshot; repository %s)", sha, abs),
		ProbeDir:    dir,
		cleanup:     dir,
	}, nil
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
			result = validateCard(ctx, root, tasksDir, card, index, &citations)
		}
		admission := AdmitCriteria(ctx, tree.ProbeDir, card.Zone, tree.Description, card.Criteria())
		replaceVacuous(&result, admission.Passing)
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

func replaceVacuous(result *ValidationResult, passing []string) {
	kept := make([]Finding, 0, len(result.Errors))
	for _, finding := range result.Errors {
		if finding.Field == "verify" && strings.Contains(finding.Message, "Criterion already passes") {
			continue
		}
		kept = append(kept, finding)
	}
	for _, text := range passing {
		kept = append(kept, Finding{"verify", fmt.Sprintf("Criterion already passes on the current tree, so completing this card would prove nothing: %s", text)})
	}
	result.Errors = kept
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
