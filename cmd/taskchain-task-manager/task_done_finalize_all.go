package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardruntime"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func taskDoneFinalizeAll(ctx context.Context, args []string, out io.Writer) error {
	asJSON, dryRun := false, false
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprint(out, doneFinalizeAllHelp)
			return nil
		case arg == "--json":
			asJSON = true
		case arg == "--dry-run":
			dryRun = true
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag: %s (valid: --dry-run, --json, --help, -h)", arg)
		default:
			return fmt.Errorf("ce task done-finalize-all takes no card arguments; use ce task done-finalize for one card")
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	source, err := boardruntime.SourceBranch(ctx, root)
	if err != nil {
		return err
	}
	if !dryRun {
		if err := boardruntime.ApplySourceGuard(ctx, root, source); err != nil {
			return fmt.Errorf("done-finalize-all: %w", err)
		}
	}
	entries, err := boardruntime.CleanupCandidates(ctx, root, source)
	if err != nil {
		return err
	}
	tasksDir := taskflow.TasksDirName()
	var live []boardruntime.CleanupEntry
	for _, entry := range entries {
		if boardruntime.LiveDonePath(tasksDir, entry.Path) {
			live = append(live, entry)
		}
	}
	if err := boardruntime.AnnotateRuntime(ctx, root, live); err != nil {
		return err
	}
	var removable, skipped []boardruntime.CleanupEntry
	for _, entry := range live {
		if entry.Removable {
			removable = append(removable, entry)
			continue
		}
		skipped = append(skipped, entry)
	}
	printf := func(format string, args ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, args...)
		}
	}
	if dryRun {
		printf("  Preview done-finalize-all against local source %s:\n", source)
		for _, entry := range removable {
			printf("  WOULD REMOVE %s\n", entry.Path)
		}
		for _, entry := range skipped {
			for _, reason := range entry.Blocked {
				printf("  SKIP %s: %s\n", entry.Path, reason)
			}
		}
		printf("  %d removable live done/ card group(s); %d skipped.\n", len(removable), len(skipped))
		if asJSON {
			return encodeFinalizeAll(out, doneFinalizeAllResult{DryRun: true, Candidates: finalizeItems(removable), Skipped: finalizeItems(skipped)})
		}
		return nil
	}
	svc := taskruntime.NewService(root, taskruntime.NewExecutor())
	applied := make([]boardruntime.CleanupEntry, 0, len(removable))
	printf("  Applying done-finalize-all against local source %s:\n", source)
	for _, entry := range removable {
		if err := boardruntime.ApplySourceGuard(ctx, root, source); err != nil {
			return fmt.Errorf("done-finalize-all: %w", err)
		}
		group := entry
		if err := svc.ProtectCleanup(ctx, cleanupAliases(group.ID, group.Path), func() error {
			return boardruntime.ApplyCleanupGroup(ctx, root, source, group)
		}); err != nil {
			return err
		}
		printf("  REMOVED %s\n", entry.Path)
		applied = append(applied, entry)
	}
	for _, entry := range skipped {
		for _, reason := range entry.Blocked {
			printf("  SKIP %s: %s\n", entry.Path, reason)
		}
	}
	printf("  %d removable live done/ card group(s); %d skipped.\n", len(applied), len(skipped))
	if asJSON {
		return encodeFinalizeAll(out, doneFinalizeAllResult{Applied: finalizeItems(applied), Skipped: finalizeItems(skipped)})
	}
	return nil
}

type doneFinalizeAllItem struct {
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	Blocked []string `json:"blocked,omitempty"`
}

type doneFinalizeAllResult struct {
	DryRun     bool                  `json:"dry_run"`
	Candidates []doneFinalizeAllItem `json:"candidates,omitempty"`
	Applied    []doneFinalizeAllItem `json:"applied,omitempty"`
	Skipped    []doneFinalizeAllItem `json:"skipped"`
}

func finalizeItems(entries []boardruntime.CleanupEntry) []doneFinalizeAllItem {
	items := make([]doneFinalizeAllItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, doneFinalizeAllItem{ID: entry.ID, Path: entry.Path, Blocked: entry.Blocked})
	}
	return items
}

func encodeFinalizeAll(out io.Writer, result doneFinalizeAllResult) error {
	return json.NewEncoder(out).Encode(result)
}

const doneFinalizeAllHelp = `Usage: taskchain-task-manager task done-finalize-all [--dry-run] [--json]

Finalize every live card in the board's done zone. Cards that fail a delivery, review, reference, or runtime gate are skipped with a reason. The default applies the removable groups.

Options:
  --dry-run   Report candidates and skips without writing
  --json      Machine-readable output
  --help, -h  Show this help
`
