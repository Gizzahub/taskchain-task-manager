package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardruntime"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func taskCleanup(ctx context.Context, args []string, out io.Writer) error {
	apply, all, asJSON := false, false, false
	var selected []string
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprint(out, cleanupHelp)
			return nil
		case arg == "--apply":
			apply = true
		case arg == "--json":
			asJSON = true
		case arg == "--all":
			all = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag: %s", arg)
			}
			selected = append(selected, arg)
		}
	}
	if apply && !all && len(selected) == 0 {
		return fmt.Errorf("cleanup --apply requires explicit card IDs/paths or --all")
	}
	if all && len(selected) != 0 {
		return fmt.Errorf("choose --all or explicit cards")
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	source, err := boardruntime.SourceBranch(ctx, root)
	if err != nil {
		return err
	}
	if apply {
		if err := boardruntime.ApplySourceGuard(ctx, root, source); err != nil {
			return err
		}
	}
	entries, err := boardruntime.CleanupCandidates(ctx, root, source)
	if err != nil {
		return err
	}
	if len(selected) > 0 {
		entries, err = selectCleanup(entries, selected)
		if err != nil {
			return err
		}
	}
	if err := boardruntime.AnnotateRuntime(ctx, root, entries); err != nil {
		return err
	}
	printf := func(format string, args ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, args...)
		}
	}
	mode := "Preview"
	if apply {
		mode = "Applying"
	}
	printf("%s finalized-card cleanup against local source %s:\n", mode, source)
	if apply {
		blocked := 0
		for _, entry := range entries {
			if entry.Removable {
				continue
			}
			blocked++
			for _, reason := range entry.Blocked {
				printf("  BLOCKED %s: %s\n", entry.Path, reason)
			}
		}
		if blocked > 0 {
			return fmt.Errorf("cleanup preflight blocked %d selected group(s); no files changed", blocked)
		}
	}
	removable := 0
	svc := taskruntime.NewService(root, taskruntime.NewExecutor())
	for _, entry := range entries {
		if !entry.Removable {
			for _, reason := range entry.Blocked {
				printf("  BLOCKED %s: %s\n", entry.Path, reason)
			}
			continue
		}
		if !apply {
			printf("  WOULD REMOVE %s\n", entry.Path)
			for _, artifact := range entry.Artifacts {
				printf("  WOULD REMOVE %s\n", artifact)
			}
			for _, dir := range entry.ArtifactDirs {
				printf("  WOULD REMOVE %s/\n", dir)
			}
			removable++
			continue
		}
		if err := boardruntime.ApplySourceGuard(ctx, root, source); err != nil {
			return err
		}
		group := entry
		if err := svc.ProtectCleanup(ctx, cleanupAliases(group.ID, group.Path), func() error {
			return boardruntime.ApplyCleanupGroup(ctx, root, source, group)
		}); err != nil {
			return err
		}
		printf("  REMOVED %s\n", entry.Path)
		removable++
	}
	if !apply {
		printf("No files changed; rerun with --apply and explicit IDs/paths (or --all) to remove selected cards.\n")
	}
	printf("%d removable finalized card group(s).\n", removable)
	if asJSON {
		enc := json.NewEncoder(out)
		return enc.Encode(map[string]any{"apply": apply, "groups": entries, "processed": removable, "source": source})
	}
	return nil
}

func selectCleanup(entries []boardruntime.CleanupEntry, selected []string) ([]boardruntime.CleanupEntry, error) {
	matched := map[string]bool{}
	var filtered []boardruntime.CleanupEntry
	for _, entry := range entries {
		keep := false
		for _, target := range selected {
			if target == entry.ID || filepath.Clean(target) == entry.Path {
				keep = true
				matched[target] = true
			}
		}
		if keep {
			filtered = append(filtered, entry)
		}
	}
	for _, target := range selected {
		if !matched[target] {
			return nil, fmt.Errorf("no finalized cleanup card matches %s", target)
		}
	}
	return filtered, nil
}

func cleanupAliases(id, path string) []string {
	base := filepath.Base(path)
	return []string{id, path, base, strings.TrimSuffix(base, filepath.Ext(base))}
}

const cleanupHelp = `Usage: taskchain-task-manager task cleanup [id|path...] [--apply] [--all] [--json]

Preview removal of finalized cards by default. --apply deletes the selected cards and their owned artefacts after they match the configured source tip.

Options:
  --apply     Delete the selected removable groups
  --all       Select every finalized group
  --json      Machine-readable output
  --help, -h  Show this help
`
