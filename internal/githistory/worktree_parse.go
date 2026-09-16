package githistory

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Worktree struct {
	Path        string `json:"path"`
	HEAD        string `json:"head"`
	Branch      string `json:"branch,omitempty"`
	Detached    bool   `json:"detached"`
	Bare        bool   `json:"bare"`
	Locked      bool   `json:"locked"`
	LockReason  string `json:"lockReason,omitempty"`
	Prunable    bool   `json:"prunable"`
	PruneReason string `json:"pruneReason,omitempty"`
}

func parseWorktrees(raw []byte) ([]Worktree, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("worktree porcelain is not UTF-8")
	}
	if len(raw) == 0 || len(raw) < 2 || raw[len(raw)-1] != 0 || raw[len(raw)-2] != 0 {
		return nil, errors.New("worktree porcelain has incomplete record delimiter")
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) < 3 || parts[len(parts)-1] != "" || parts[len(parts)-2] != "" {
		return nil, errors.New("worktree porcelain has incomplete record delimiter")
	}
	parts = parts[:len(parts)-1]
	var out []Worktree
	paths := map[string]bool{}
	for len(parts) > 0 {
		if parts[0] == "" {
			return nil, errors.New("empty worktree record")
		}
		end := 0
		for end < len(parts) && parts[end] != "" {
			end++
		}
		if end == len(parts) {
			return nil, errors.New("worktree record missing delimiter")
		}
		if len(out) == 256 {
			return nil, errors.New("too many worktrees")
		}
		wt, err := parseWorktreeFields(parts[:end])
		if err != nil {
			return nil, err
		}
		if paths[wt.Path] {
			return nil, fmt.Errorf("duplicate worktree path %q", wt.Path)
		}
		paths[wt.Path] = true
		out = append(out, wt)
		parts = parts[end+1:]
	}
	if len(out) == 0 {
		return nil, errors.New("worktree porcelain is empty")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func parseWorktreeFields(fields []string) (Worktree, error) {
	var wt Worktree
	seen := map[string]bool{}
	for _, field := range fields {
		name, value := field, ""
		if i := strings.IndexByte(field, ' '); i >= 0 {
			name, value = field[:i], field[i+1:]
		}
		if seen[name] {
			return Worktree{}, fmt.Errorf("duplicate worktree attribute %q", name)
		}
		seen[name] = true
		switch name {
		case "worktree":
			if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
				return Worktree{}, errors.New("invalid worktree path")
			}
			wt.Path = value
		case "HEAD":
			if !objectID.MatchString(value) {
				return Worktree{}, errors.New("invalid worktree HEAD")
			}
			wt.HEAD = value
		case "branch":
			if value == "" {
				return Worktree{}, errors.New("empty worktree branch")
			}
			wt.Branch = value
		case "detached":
			if field != "detached" {
				return Worktree{}, errors.New("invalid detached field")
			}
			wt.Detached = true
		case "bare":
			if field != "bare" {
				return Worktree{}, errors.New("invalid bare field")
			}
			wt.Bare = true
		case "locked":
			wt.Locked = true
			wt.LockReason = value
		case "prunable":
			wt.Prunable = true
			wt.PruneReason = value
		default:
			return Worktree{}, fmt.Errorf("unknown worktree attribute %q", name)
		}
	}
	if wt.Path == "" {
		return Worktree{}, errors.New("worktree record missing path")
	}
	if wt.Bare {
		if wt.HEAD != "" || wt.Branch != "" || wt.Detached {
			return Worktree{}, errors.New("bare worktree has incompatible attributes")
		}
		return wt, nil
	}
	if wt.HEAD == "" {
		return Worktree{}, errors.New("worktree record missing HEAD")
	}
	if wt.Detached == (wt.Branch != "") {
		return Worktree{}, errors.New("worktree requires branch or detached state")
	}
	return wt, nil
}
