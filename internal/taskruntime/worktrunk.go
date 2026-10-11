package taskruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var worktrunkVersionReport = regexp.MustCompile(`^wt ([0-9]+)\.([0-9]+)\.([0-9]+)$`)

func worktrunkVersionSupported(report string) bool {
	match := worktrunkVersionReport.FindStringSubmatch(report)
	if match == nil {
		return false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	if major != worktrunkMinMajor {
		return major > worktrunkMinMajor
	}
	return minor >= worktrunkMinMinor
}

type worktrunkInventory struct {
	SchemaVersion   int
	Paths           map[string]string
	SkippedNoBranch int
	SkippedNoPath   int
	Diagnostic      CommandResult
}

func worktreePathOverride(path string) string {
	return "worktree-path = " + strconv.Quote(filepath.ToSlash(path))
}

// worktrunkList reads the schema 2 inventory. A bad envelope is an error.
// An item with no branch, or a branch and no path, is skipped and counted.
// An item missing both counts as no branch: it never had a lookup key.
func (s *Service) worktrunkList(ctx context.Context) (worktrunkInventory, error) {
	result, err := s.command(ctx, s.root, "wt", "--config-set", "list.json-schema = 2", "list", "--format=json")
	if err != nil {
		return worktrunkInventory{}, err
	}
	if result.ExitCode != 0 {
		return worktrunkInventory{Diagnostic: result}, fmt.Errorf("worktrunk list failed: %s", strings.TrimSpace(result.Stderr))
	}
	var payload struct {
		Schema int `json:"schema"`
		Items  []struct {
			Branch   string `json:"branch"`
			Worktree struct {
				Path string `json:"path"`
			} `json:"worktree"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return worktrunkInventory{Diagnostic: result}, fmt.Errorf("decode Worktrunk schema 2: %w", err)
	}
	if payload.Schema != 2 || payload.Items == nil {
		return worktrunkInventory{SchemaVersion: payload.Schema, Diagnostic: result}, fmt.Errorf("worktrunk list must return a schema 2 items envelope")
	}
	paths := map[string]string{}
	skippedNoBranch, skippedNoPath := 0, 0
	for _, item := range payload.Items {
		if strings.TrimSpace(item.Branch) == "" {
			skippedNoBranch++
			continue
		}
		if strings.TrimSpace(item.Worktree.Path) == "" {
			skippedNoPath++
			continue
		}
		absolute, err := filepath.Abs(item.Worktree.Path)
		if err != nil {
			return worktrunkInventory{SchemaVersion: payload.Schema, Diagnostic: result}, fmt.Errorf("resolve Worktrunk path: %w", err)
		}
		paths[item.Branch] = absolute
	}
	return worktrunkInventory{SchemaVersion: payload.Schema, Paths: paths, SkippedNoBranch: skippedNoBranch, SkippedNoPath: skippedNoPath, Diagnostic: result}, nil
}
