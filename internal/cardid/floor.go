package cardid

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// frontmatterIDLine matches an `id:` frontmatter line and captures the raw
// scalar. The value must be followed by a YAML comment or nothing, so
// `id: TASK-1 and PLAN-2` — a sentence, not a declaration — matches nowhere.
// One matching quote pair is stripped before parsing, because a quoted
// `id: 'TASK-012'` is the same claim in another spelling.
var frontmatterIDLine = regexp.MustCompile(`(?m)^id:[ \t]*(\S+)(?:[ \t]+#[^\n]*)?[ \t]*\r?$`)

// HighestFrontmatterID returns the largest number the document's leading
// frontmatter block gives a canonical ID of prefix, and 0 when it names
// none. Only the leading block is this document's own id: an `id:` line in
// the body — a fenced reproduction block quoting an old probe card, say — is
// prose about some other card, and counting it would raise the floor from
// any sentence that starts a line with `id:`.
func HighestFrontmatterID(content, prefix string) int {
	highest := 0
	for _, m := range frontmatterIDLine.FindAllStringSubmatch(leadingFrontmatter(content), -1) {
		parsed, err := Parse(unquoteYAMLScalar(m[1]))
		if err != nil || parsed.Prefix != prefix {
			continue
		}
		if parsed.Number > uint64(highest) {
			highest = int(parsed.Number)
		}
	}
	return highest
}

// leadingFrontmatter returns the text between the opening `---` fence and
// its closing fence, fences excluded, or "" when the document does not open
// with a terminated frontmatter block. Everything after that fence is body.
func leadingFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[1:i], "\n")
		}
	}
	return ""
}

func unquoteYAMLScalar(s string) string {
	if len(s) < 2 {
		return s
	}
	if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
		return s[1 : len(s)-1]
	}
	return s
}

// ScanFloor walks tasksDir and returns the highest number any card's leading
// frontmatter claims for prefix. A file that cannot be read contributes
// nothing rather than failing the scan: one unreadable card must not block
// every creation. A missing board directory is an empty board, floor 0.
func ScanFloor(tasksDir, prefix string) (int, error) {
	highest := 0
	err := filepath.WalkDir(tasksDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == tasksDir && os.IsNotExist(err) {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if n := HighestFrontmatterID(string(content), prefix); n > highest {
			highest = n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return highest, nil
}
