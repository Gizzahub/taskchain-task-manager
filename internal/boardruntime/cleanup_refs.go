package boardruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type referenceFile struct {
	path    string
	content []byte
	tracked bool
}

func ownedArtifacts(root, tasksDir, id, cardPath string) ([]string, []string, error) {
	if _, err := parseCanonical(id); err != nil {
		return nil, nil, fmt.Errorf("id is not canonical")
	}
	roots := []string{
		filepath.ToSlash(filepath.Join(tasksDir, "receipts", id)),
		filepath.ToSlash(filepath.Join("docs", "task-reviews", id)),
	}
	var files, dirs []string
	for _, rel := range roots {
		if err := contained(root, rel, true); err != nil {
			return files, dirs, err
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return files, dirs, fmt.Errorf("cannot inspect owned artifact %s: %w", rel, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return files, dirs, fmt.Errorf("owned artifact must be a non-symlink directory: %s", rel)
		}
		err = filepath.WalkDir(full, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
				return fmt.Errorf("owned artifact contains symlink: %s", rel)
			}
			got, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			got = filepath.ToSlash(got)
			if entry.IsDir() {
				dirs = append(dirs, got)
				return nil
			}
			files = append(files, got)
			return nil
		})
		if err != nil {
			return files, dirs, err
		}
	}
	flat := filepath.ToSlash(filepath.Join("docs", "task-reviews", id+".md"))
	bound, err := pathBound(root, cardPath, files, flat)
	if err != nil {
		return files, dirs, err
	}
	if bound {
		if err := contained(root, flat, false); err != nil {
			return files, dirs, err
		}
		files = append(files, flat)
	} else if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(flat))); statErr == nil {
		return files, dirs, fmt.Errorf("flat review requires explicit artifact binding: %s", flat)
	} else if !os.IsNotExist(statErr) {
		return files, dirs, statErr
	}
	sortLongest(dirs)
	return files, dirs, nil
}

func pathBound(root, cardPath string, artifacts []string, want string) (bool, error) {
	paths := append([]string{cardPath}, artifacts...)
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		if strings.Contains(string(data), want) {
			return true, nil
		}
	}
	return false, nil
}

func parseCanonical(id string) (struct{ Prefix string }, error) {
	parsed, err := canonicalID(id)
	if err != nil {
		return struct{ Prefix string }{}, err
	}
	return struct{ Prefix string }{Prefix: parsed}, nil
}

func canonicalID(id string) (string, error) {
	prefix, digits, ok := strings.Cut(id, "-")
	if !ok || digits == "" {
		return "", fmt.Errorf("invalid")
	}
	switch prefix {
	case "TASK", "PLAN", "ISSUE", "BACKLOG":
	default:
		return "", fmt.Errorf("invalid")
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("invalid")
		}
	}
	return prefix, nil
}

func referenceFiles(ctx context.Context, root string, tracked map[string]bool) ([]referenceFile, error) {
	out, err := git(ctx, root, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	var paths, oids []string
	for _, record := range bytes.Split(out, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		pair := bytes.SplitN(record, []byte{'\t'}, 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("invalid Git index record")
		}
		fields := strings.Fields(string(pair[0]))
		if len(fields) != 3 || fields[2] != "0" {
			return nil, fmt.Errorf("cleanup refuses unmerged index")
		}
		if fields[0] == "160000" {
			continue
		}
		paths = append(paths, string(pair[1]))
		oids = append(oids, fields[1])
	}
	files, err := indexBlobs(ctx, root, paths, oids, tracked)
	if err != nil {
		return nil, err
	}
	work, err := worktreeRefs(root, tracked)
	if err != nil {
		return nil, err
	}
	return append(files, work...), nil
}

func indexBlobs(ctx context.Context, root string, paths, oids []string, tracked map[string]bool) ([]referenceFile, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	var total int64
	for _, oid := range oids {
		sizeRaw, err := git(ctx, root, "cat-file", "-s", oid)
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(sizeRaw)), 10, 64)
		if err != nil || size < 0 || size > 16<<20 {
			return nil, fmt.Errorf("cleanup index blob exceeds inspection limit")
		}
		total += size
		if total > 64<<20 {
			return nil, fmt.Errorf("cleanup text inspection exceeds 64 MiB aggregate limit")
		}
	}
	var files []referenceFile
	for i, path := range paths {
		data, err := git(ctx, root, "cat-file", "blob", oids[i])
		if err != nil {
			return nil, err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		files = append(files, referenceFile{path: filepath.ToSlash(path), content: data, tracked: tracked[filepath.ToSlash(path)]})
	}
	return files, nil
}

func worktreeRefs(root string, tracked map[string]bool) ([]referenceFile, error) {
	var files []referenceFile
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".ce-task-cleanup.lock") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) {
				target = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(rel), target)))
			}
			files = append(files, referenceFile{path: rel, content: []byte(filepath.ToSlash(target)), tracked: tracked[rel]})
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			if info.Size() > 16<<20 {
				return fmt.Errorf("cleanup reference too large: %s", rel)
			}
			return nil
		}
		total += info.Size()
		if total > 64<<20 {
			return fmt.Errorf("cleanup text inspection exceeds 64 MiB aggregate limit")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(content, 0) >= 0 {
			return nil
		}
		files = append(files, referenceFile{path: rel, content: content, tracked: tracked[rel]})
		return nil
	})
	return files, err
}

func namesEntry(tasksDir string, ref referenceFile, entry CleanupEntry) (bool, string) {
	if taskCardPath(tasksDir, ref.path) {
		return false, ""
	}
	targets := append([]string{entry.Path}, entry.Artifacts...)
	targets = append(targets, entry.ArtifactDirs...)
	for _, target := range targets {
		if target != "" && bytes.Contains(ref.content, []byte(target)) {
			return true, "path string"
		}
	}
	return false, ""
}

func taskCardPath(tasksDir, path string) bool {
	path = filepath.ToSlash(path)
	prefix := filepath.ToSlash(tasksDir) + "/"
	base := filepath.Base(path)
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, ".md") &&
		base != "README.md" && base != "INDEX.md" && base != "TEMPLATE.md"
}

func durableReviewReceipt(root, tasksDir, path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	prefix := filepath.ToSlash(filepath.Join(tasksDir, "done", "evidence")) + "/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || !strings.HasPrefix(parts[1], "done-review-") || !strings.HasSuffix(parts[1], ".json") {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return false
	}
	var receipt struct {
		Reviewed string `json:"reviewed-card-sha256"`
	}
	if json.Unmarshal(data, &receipt) != nil || len(receipt.Reviewed) != 64 {
		return false
	}
	for _, r := range receipt.Reviewed {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
