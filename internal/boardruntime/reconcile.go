package boardruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

// ReconcileResult is the reconcile report. JSON is encoded even when the
// returned error is non-nil.
type ReconcileResult struct {
	Applied bool            `json:"applied"`
	DryRun  bool            `json:"dry_run"`
	Cards   []ReconcileCard `json:"cards"`
}

// ReconcileCard is one doing card. Unexported fields stay out of JSON.
type ReconcileCard struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Recovery    string `json:"recovery,omitempty"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
	content     []byte
	execution   *taskruntime.Execution
	receipt     *taskruntime.Receipt
}

// Reconcile moves interrupted doing cards. dryRun writes nothing and does not
// take the mutation lock. Apply takes the lock and rescans each card.
func Reconcile(ctx context.Context, root, tasksDir string, dryRun bool) (ReconcileResult, error) {
	result := ReconcileResult{DryRun: dryRun, Cards: []ReconcileCard{}}
	tasksDir, err := containedTasks(root, tasksDir)
	if err != nil {
		return result, err
	}
	svc := taskruntime.NewService(root, taskruntime.NewExecutor())
	if dryRun {
		executions, receipts, err := svc.ReadLedger(ctx)
		if err != nil {
			return result, err
		}
		cards, err := reconcileCandidates(root, tasksDir, executions, receipts)
		result.Cards = cards
		return result, err
	}
	var failures []error
	err = svc.ProtectLock(ctx, func() error {
		executions, receipts, err := svc.ReadLedger(ctx)
		if err != nil {
			return err
		}
		cards, err := reconcileCandidates(root, tasksDir, executions, receipts)
		if err != nil {
			return err
		}
		result.Cards = cards
		recovery, err := svc.BoardRecoveryDir(ctx)
		if err != nil {
			return stopReconcile(cards, err)
		}
		for i := range cards {
			if cards[i].Action != "move-to-blocked" && cards[i].Action != "move-to-review" {
				continue
			}
			liveExec, liveReceipts, liveErr := svc.ReadLedger(ctx)
			if liveErr != nil {
				return stopReconcile(cards[i:], liveErr)
			}
			live, scanErr := reconcileCandidates(root, tasksDir, liveExec, liveReceipts)
			if scanErr != nil {
				return stopReconcile(cards[i:], scanErr)
			}
			current := matchCard(live, cards[i].Path)
			if current == nil || current.Action != cards[i].Action || !bytes.Equal(current.content, cards[i].content) || !sameEvidence(*current, cards[i]) {
				cards[i].Action, cards[i].Reason = "preserved", "card or execution evidence changed before application"
				continue
			}
			zone := "blocked"
			heading := "Runtime interruption evidence"
			if cards[i].Action == "move-to-review" {
				zone = "review"
				heading = "Runtime completion evidence"
			}
			dest, applied, moveErr := reconcileMove(root, cards[i], recovery, zone, heading)
			cards[i].Recovery = dest
			result.Applied = result.Applied || applied
			if moveErr != nil {
				cards[i].Action, cards[i].Reason = "error", moveErr.Error()
				failures = append(failures, fmt.Errorf("%s: %w", cards[i].Path, moveErr))
				continue
			}
			if zone == "blocked" {
				cards[i].Action, cards[i].Reason = "moved-to-blocked", "valid abort receipt permits board transition; original and interruption evidence retained"
			} else {
				cards[i].Action, cards[i].Reason = "moved-to-review", "valid finish receipt and completion criteria verified; original and completion evidence retained"
			}
		}
		result.Cards = cards
		return errors.Join(failures...)
	})
	if result.Cards == nil {
		result.Cards = []ReconcileCard{}
	}
	return result, err
}

func stopReconcile(pending []ReconcileCard, failures ...error) error {
	err := errors.Join(failures...)
	for i := range pending {
		if strings.HasPrefix(pending[i].Action, "move-to-") {
			pending[i].Action, pending[i].Reason = "not-attempted", err.Error()
		}
	}
	return err
}

func matchCard(cards []ReconcileCard, path string) *ReconcileCard {
	for i := range cards {
		if cards[i].Path == path {
			return &cards[i]
		}
	}
	return nil
}

func sameEvidence(a, b ReconcileCard) bool {
	if a.execution == nil || b.execution == nil || a.receipt == nil || b.receipt == nil {
		return false
	}
	return *a.execution == *b.execution && a.receipt.Task == b.receipt.Task && a.receipt.CreatedAt.Equal(b.receipt.CreatedAt) &&
		a.receipt.Status == b.receipt.Status && a.receipt.Reason == b.receipt.Reason && a.receipt.Operation == b.receipt.Operation
}

func reconcileCandidates(root, tasksDir string, executions []taskruntime.Execution, receipts map[string]taskruntime.Receipt) ([]ReconcileCard, error) {
	cards, err := taskflow.FindCardsIn(root, tasksDir, true)
	if err != nil {
		return nil, fmt.Errorf("find board cards: %w", err)
	}
	type item struct {
		id, path, identity string
		content            []byte
	}
	byID := map[string][]item{}
	allIDs := map[string]int{}
	var unknown []ReconcileCard
	rules := completionRules()
	for _, cardFile := range cards {
		if cardFile == nil {
			continue
		}
		path := filepath.ToSlash(cardFile.RepoRel())
		parsed, valid := cardid.Parse(cardFile.ID)
		identity := ""
		if valid == nil {
			identity = parsed.Key()
			allIDs[identity]++
		}
		if cardFile.Zone != "doing" {
			continue
		}
		row := ReconcileCard{ID: cardFile.ID, Path: path, Action: "preserved"}
		if valid != nil || parsed.Prefix != "TASK" {
			row.Reason = "missing or invalid canonical task ID"
			unknown = append(unknown, row)
			continue
		}
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if !readableCard(root, absolute) {
			row.Reason = "symlinked or unreadable card path"
			unknown = append(unknown, row)
			continue
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return nil, err
		}
		key := taskruntime.NormalizeIdentity(cardFile.ID)
		byID[key] = append(byID[key], item{id: cardFile.ID, path: path, identity: identity, content: content})
	}
	byExecution := map[string][]taskruntime.Execution{}
	for _, execution := range executions {
		if key := taskruntime.NormalizeIdentity(execution.Task); key != "" {
			byExecution[key] = append(byExecution[key], execution)
		}
	}
	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := append([]ReconcileCard{}, unknown...)
	for _, key := range keys {
		group := byID[key]
		if len(group) != 1 || allIDs[group[0].identity] != 1 {
			for _, c := range group {
				out = append(out, ReconcileCard{ID: c.id, Path: c.path, Action: "preserved", Reason: "duplicate canonical card ID"})
			}
			continue
		}
		c := group[0]
		row := ReconcileCard{ID: c.id, Path: c.path, Action: "preserved", content: c.content}
		matches := byExecution[key]
		if len(matches) == 0 {
			row.Reason = "no matching runtime execution"
			out = append(out, row)
			continue
		}
		if len(matches) != 1 {
			row.Reason = "ambiguous matching runtime executions"
			out = append(out, row)
			continue
		}
		execution := matches[0]
		row.execution = &execution
		if execution.Owner.Actor == "" || execution.Owner.Host == "" || execution.Owner.Kind == "" || execution.Branch == "" || execution.Worktree == "" || execution.StartedAt.IsZero() {
			row.Reason = "incomplete runtime execution identity"
			out = append(out, row)
			continue
		}
		receipt, ok := receipts[execution.Task]
		if !ok {
			row.Reason = "missing runtime receipt"
			out = append(out, row)
			continue
		}
		row.receipt = &receipt
		if receipt.CreatedAt.IsZero() || !taskruntime.ReceiptBindsExecution(execution, receipt) {
			row.Reason = "receipt does not bind this execution"
			out = append(out, row)
			continue
		}
		if taskruntime.AbortReceiptBinds(execution, receipt) {
			dest := replaceZone(c.path, "blocked")
			row.Destination, row.Action, row.Reason = dest, "move-to-blocked", "valid abort receipt permits board transition"
			out = append(out, row)
			continue
		}
		if taskruntime.DoneReceiptBinds(execution, receipt) {
			dest := replaceZone(c.path, "review")
			row.Destination = dest
			rewritten, cell := taskflow.RewriteStatusCellText(string(c.content), taskflow.StatusReview)
			if !cell || !completionOK(dest, []byte(rewritten), rules) {
				row.Action, row.Reason = "preserved", "completion criteria or schema validation failed"
			} else {
				row.Action, row.Reason = "move-to-review", "valid finish receipt and completion criteria verified"
			}
			out = append(out, row)
			continue
		}
		row.Reason = "runtime execution is active, unresolved, or has non-terminal evidence"
		out = append(out, row)
	}
	if out == nil {
		out = []ReconcileCard{}
	}
	return out, nil
}

func replaceZone(path, zone string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if index, _, ok := taskflow.ZoneSegment(parts); ok {
		parts[index] = zone
	}
	return strings.Join(parts, "/")
}

func readableCard(root, absolute string) bool {
	physical, err := filepath.EvalSymlinks(absolute)
	rootReal, rootErr := filepath.EvalSymlinks(root)
	relative, relErr := filepath.Rel(root, absolute)
	if err != nil || rootErr != nil || relErr != nil {
		return false
	}
	return physical == filepath.Join(rootReal, relative)
}

func completionOK(path string, content []byte, rules card.ValidationRules) bool {
	doc, err := card.Parse(content)
	if err != nil {
		return false
	}
	report, err := doc.ValidateCompletion(path, rules)
	return err == nil && report.Valid
}

func completionRules() card.ValidationRules {
	return card.ValidationRules{
		Name: "ce", IDRequired: true, SummaryHeading: "Summary", CriteriaHeading: "Completion Criteria",
		PriorityValues:   []string{"P0", "P1", "P2", "P3"},
		TaskTypes:        []string{"feature", "bug", "chore", "refactor", "cleanup", "docs", "test"},
		FilenamePrefixes: []string{"P0", "P1", "P2", "P3"},
	}
}

func containedTasks(root, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		value = taskflow.TasksDirName()
	}
	if filepath.IsAbs(value) {
		return "", fmt.Errorf("tasks directory must be a relative path")
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tasks directory escapes repository root: %q", value)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository for tasks containment: %w", err)
	}
	tasksPath := filepath.Join(root, clean)
	info, err := os.Lstat(tasksPath)
	if os.IsNotExist(err) {
		return filepath.ToSlash(clean), nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("tasks directory escapes repository through a symlink: %q", value)
	}
	real, err := filepath.EvalSymlinks(tasksPath)
	if err != nil {
		return "", fmt.Errorf("resolve tasks directory for containment: %w", err)
	}
	if real != filepath.Join(rootReal, clean) {
		return "", fmt.Errorf("tasks directory escapes repository through a symlink: %q", value)
	}
	return filepath.ToSlash(clean), nil
}
