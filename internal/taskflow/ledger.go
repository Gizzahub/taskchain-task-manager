package taskflow

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RefFloor scans git history for the highest number cards of prefix carry on
// any ref. Like the pinned scanner, a failure is returned, never folded into
// zero: a floor that silently reports empty reissues numbers.
func RefFloor(ctx context.Context, root, prefix string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotepath=false", "--no-replace-objects",
		"--no-lazy-fetch", "log", "--all", "--format=%s %b", "--", tasksDirName())
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return 0, fmt.Errorf("git log --all: %w: %s", err, msg)
		}
		return 0, fmt.Errorf("git log --all: %w", err)
	}
	highest := 0
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + `-(\d+)`)
	for _, m := range re.FindAllStringSubmatch(string(out), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
			highest = n
		}
	}
	return highest, nil
}

// GitCommonDir resolves the git common directory of root, reporting whether
// one exists. Without a repository there is no shared ledger and the creator
// falls back to its own tree scan. The call is bounded: a wedged git
// credential helper or hook must not hang every creation.
func GitCommonDir(root string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir), true
}
