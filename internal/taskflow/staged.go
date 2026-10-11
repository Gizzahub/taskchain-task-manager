package taskflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// StagedItem is one staged-validation finding or warning.
type StagedItem struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// MaterializeIndex copies index blobs for ce-tasks.yaml, .ce/task-runtime.yaml,
// and tasks/** into a temp directory. Worktree edits are not read.
func MaterializeIndex(ctx context.Context, root string) (string, error) {
	rawBytes, err := gitBytes(ctx, root, "ls-files", "--stage", "-z")
	if err != nil {
		return "", fmt.Errorf("read Git index: %w", err)
	}
	raw := string(rawBytes)
	tmp, err := os.MkdirTemp("", "ce-task-index-")
	if err != nil {
		return "", fmt.Errorf("make index snapshot: %w", err)
	}
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	for _, entry := range strings.Split(raw, "\x00") {
		if entry == "" {
			continue
		}
		metaPath := strings.SplitN(entry, "\t", 2)
		if len(metaPath) != 2 {
			return fail(fmt.Errorf("parse Git index entry"))
		}
		meta := strings.Fields(metaPath[0])
		if len(meta) != 3 {
			return fail(fmt.Errorf("parse Git index metadata"))
		}
		path := metaPath[1]
		if err := acceptIndexEntry(meta[0], meta[2], path); err != nil {
			return fail(err)
		}
		if !stagedPathWanted(path) {
			continue
		}
		blob, blobErr := gitBytes(ctx, root, "cat-file", "blob", meta[1])
		if blobErr != nil {
			return fail(fmt.Errorf("read index blob for %s: %w", path, blobErr))
		}
		destination := filepath.Join(tmp, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return fail(fmt.Errorf("create snapshot directory: %w", err))
		}
		if err := os.WriteFile(destination, blob, 0o644); err != nil {
			return fail(fmt.Errorf("write snapshot blob: %w", err))
		}
	}
	return tmp, nil
}

func stagedPathWanted(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean == "ce-tasks.yaml" || clean == ".ce/task-runtime.yaml" || strings.HasPrefix(clean, "tasks/")
}

func acceptIndexEntry(mode, stage, path string) error {
	if stage != "0" {
		return fmt.Errorf("unmerged Git index entry: %s", path)
	}
	clean := filepath.Clean(path)
	if clean == "." || filepath.IsAbs(path) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return fmt.Errorf("unsafe Git index path: %q", path)
	}
	if !stagedPathWanted(path) {
		return nil
	}
	if !strings.HasPrefix(mode, "100") {
		return fmt.Errorf("task/config index entry must be a regular file, got mode %s for %s", mode, path)
	}
	return nil
}

// ValidateStaged runs the board validator against an index snapshot.
// Warnings other than quality-review-receipt are dropped. Admission is not
// probed: the snapshot is not a git repository.
func ValidateStaged(ctx context.Context, snapshot string) (findings, warnings []StagedItem, err error) {
	if err := CheckConfigYAML(snapshot); err != nil {
		return nil, nil, err
	}
	results, _, err := ValidateAllIn(ctx, snapshot, "tasks", cardCheck{skipVacuousProbe: true})
	if err != nil {
		return nil, nil, err
	}
	findings = []StagedItem{}
	warnings = []StagedItem{}
	for _, result := range results {
		for _, issue := range result.Errors {
			findings = append(findings, StagedItem{Path: filepath.ToSlash(result.Path), Kind: issue.Field, Message: issue.Message})
		}
		for _, issue := range result.Warnings {
			if issue.Field == "quality-review-receipt" {
				warnings = append(warnings, StagedItem{Path: filepath.ToSlash(result.Path), Kind: issue.Field, Message: issue.Message})
			}
		}
	}
	return findings, warnings, nil
}

// RenderStaged writes the text or JSON staged report. The text tool stamp is
// printed by the caller before validation, so a dialect error still shows it.
// jsonOut is one object and has no stamp line.
func RenderStaged(w io.Writer, findings, warnings []StagedItem, jsonOut bool) error {
	if findings == nil {
		findings = []StagedItem{}
	}
	if warnings == nil {
		warnings = []StagedItem{}
	}
	if jsonOut {
		payload := struct {
			SchemaVersion int          `json:"schemaVersion"`
			Mode          string       `json:"mode"`
			ToolVersion   string       `json:"tool_version"`
			ToolRevision  string       `json:"tool_revision"`
			Findings      []StagedItem `json:"findings"`
			Warnings      []StagedItem `json:"warnings"`
		}{2, "staged", ReferenceStampVersion, ReferenceStampRevision, findings, warnings}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fmt.Errorf("encode staged report: %w", err)
		}
		fmt.Fprintln(w, string(data))
		return nil
	}
	if len(findings) == 0 {
		fmt.Fprintln(w, "Staged task snapshot: valid")
	}
	for _, finding := range findings {
		fmt.Fprintf(w, "%s [%s]: %s\n", finding.Path, finding.Kind, finding.Message)
	}
	for _, warning := range warnings {
		fmt.Fprintf(w, "%s [%s]: warning: %s\n", warning.Path, warning.Kind, warning.Message)
	}
	return nil
}
