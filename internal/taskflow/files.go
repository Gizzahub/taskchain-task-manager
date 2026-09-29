package taskflow

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// stampFrontmatterField writes one `key: value` pair into a file's frontmatter,
// replacing an existing line in place or inserting before the closing fence.
// It reports whether the file's bytes changed.
func stampFrontmatterField(path, key, value string) (bool, error) {
	return stampFrontmatterFields(path, [][2]string{{key, value}})
}

func stampFrontmatterFields(path string, pairs [][2]string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read card for frontmatter stamp: %w", err)
	}
	lines := bytes.SplitAfter(content, []byte("\n"))
	if len(lines) == 0 || strings.TrimRight(string(lines[0]), "\r\n") != "---" {
		return false, nil
	}
	// The closing fence is found before anything is written, in a pass of its
	// own: in a file whose block never closes, a single pass would rewrite a
	// line it has not established is frontmatter.
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(string(lines[i]), "\r\n") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return false, nil
	}
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		prefix := []byte(key + ":")
		entry := []byte(key + ": " + value + "\n")
		replaced := false
		for i := 1; i < closing; i++ {
			if bytes.HasPrefix(lines[i], prefix) {
				lines[i] = entry
				replaced = true
				break
			}
		}
		if replaced {
			continue
		}
		// The key was absent, so it is inserted at the closing fence: appending
		// keeps the author's ordering intact, and the fence index moves with it
		// so a later key in the same batch still lands inside the block.
		updated := append([][]byte{}, lines[:closing]...)
		updated = append(updated, entry)
		updated = append(updated, lines[closing:]...)
		lines = updated
		closing++
	}
	rewritten := bytes.Join(lines, nil)
	if bytes.Equal(rewritten, content) {
		return false, nil
	}
	if err := writePreservingMode(path, rewritten); err != nil {
		return false, err
	}
	return true, nil
}

func writePreservingMode(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return fmt.Errorf("write card: %w", err)
	}
	return nil
}

// The cell is matched with its trailing run of spaces before the next pipe
// kept separate, so the rewrite changes the cell's words and not the row's
// column padding.
var statusCellRe = regexp.MustCompile(`(\*\*Status\*\*\s*\|\s*)([^|\n]*?)(\s*\|)`)

// rewriteStatusCell rewrites the first **Status** table cell outside fences to
// the status's canonical cell text. It reports whether a cell was found.
func rewriteStatusCell(path string, status Status) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read card for status sync: %w", err)
	}
	updated, replaced := replaceFirstStatusCellOutsideFences(string(content), status)
	if !replaced {
		return false, nil
	}
	if err := writePreservingMode(path, []byte(updated)); err != nil {
		return false, err
	}
	return true, nil
}

func replaceFirstStatusCellOutsideFences(content string, status Status) (string, bool) {
	var fences FenceScanner
	var out strings.Builder
	replaced := false
	for _, raw := range strings.SplitAfter(content, "\n") {
		if !replaced && fences.Classify(raw) == OutsideFence {
			if loc := statusCellRe.FindStringSubmatchIndex(raw); loc != nil {
				// loc[4]:loc[5] is the old cell text; everything before it is
				// the `**Status** | ` separator, loc[6]: is the row's spacing
				// and remaining columns. Only the cell text is rewritten.
				out.WriteString(raw[:loc[4]])
				out.WriteString(status.StatusCell())
				out.WriteString(raw[loc[6]:])
				replaced = true
				continue
			}
		}
		out.WriteString(raw)
	}
	if !replaced {
		return content, false
	}
	return out.String(), true
}

// moveFile relocates one file, preferring `git mv` when the file is tracked so
// history follows the card, and falling back to a plain rename otherwise.
func moveFile(root, src, dst string) error {
	if gitMoves(root, src, dst) {
		return nil
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move card: %w", err)
	}
	return nil
}

func gitMoves(root, src, dst string) bool {
	cmd := exec.Command("git", "-C", root, "mv", "--", src, dst)
	// A failed attempt (no repository, untracked file) is a silent fallback to
	// rename, never a leak into the verb's stderr.
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// writeNewFile creates a file that must not already exist, creating the parent
// directory when needed.
func writeNewFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create card directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("write card: %w", err)
	}
	return nil
}

// relUnderTasks reports whether path sits inside the tasks directory and
// returns that path relative to it. A "./" or "../" prefix refuses.
func relUnderTasks(path string) (string, bool) {
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", false
	}
	rel, err := filepath.Rel(TasksDir, cleaned)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
