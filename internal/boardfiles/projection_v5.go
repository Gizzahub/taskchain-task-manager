package boardfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const projectionPrefix = "<!-- task-manager:projection-"
const maxProjectionBytes = 1 << 20

func projectionPathIssue(rel string) string {
	if !utf8.ValidString(rel) || !fs.ValidPath(rel) || rel == "." ||
		strings.TrimSpace(rel) != rel || strings.Contains(rel, "\\") ||
		!filepath.IsLocal(filepath.FromSlash(rel)) || filepath.ToSlash(filepath.FromSlash(rel)) != rel ||
		strings.Contains(rel, projectionPrefix) || strings.Contains(rel, "-->") {
		return "live card path cannot round-trip in live-card-paths-v1"
	}
	for _, r := range rel {
		if unicode.IsControl(r) {
			return "live card path cannot round-trip in live-card-paths-v1"
		}
	}
	return ""
}

// projectionMarkers is the sole owner of the exact marker wire grammar.
// JSON string encoding escapes values without interpreting them as templates.
func projectionMarkers(role boardpolicy.FileRoleV5) (begin, end []byte) {
	quoted := func(value string) string {
		raw, _ := json.Marshal(value) // Strings always marshal; policy validates UTF-8.
		return string(raw)
	}
	identity := " format=" + quoted(role.Format) + " owner=" + quoted(role.Owner) +
		" source-revision=" + quoted(role.SourceRevision) + " -->\n"
	return []byte(projectionPrefix + "begin" + identity), []byte(projectionPrefix + "end" + identity)
}

func projectionIssue(data []byte, role boardpolicy.FileRoleV5, live []string) string {
	begin, end := projectionMarkers(role)
	if bytes.Count(data, []byte(projectionPrefix)) != 2 ||
		bytes.Count(data, begin) != 1 || bytes.Count(data, end) != 1 {
		return "projection requires exactly one matching begin and end marker"
	}
	start, finish := bytes.Index(data, begin), bytes.Index(data, end)
	if start >= finish || (start > 0 && data[start-1] != '\n') || (finish > 0 && data[finish-1] != '\n') {
		return "projection markers must be ordered complete LF-terminated lines"
	}
	var expected strings.Builder
	for _, rel := range live {
		expected.WriteString("- ")
		expected.WriteString(rel)
		expected.WriteByte('\n')
	}
	if !bytes.Equal(data[start+len(begin):finish], []byte(expected.String())) {
		return "projection content differs from live-card-paths-v1 inventory"
	}
	return ""
}

func declaredRoleV5Issue(r *os.Root, role boardpolicy.FileRoleV5, live []string) (string, error) {
	limit := int64(64 << 10)
	if role.Kind == boardpolicy.FileRoleProjection {
		limit = maxProjectionBytes
	}
	data, issue, err := readDeclaredFile(r, role.Path, limit)
	if err != nil {
		return "", fmt.Errorf("read declared file %s: %w", role.Path, err)
	}
	if issue != "" {
		return issue, nil
	}
	if role.Kind == boardpolicy.FileRoleProjection {
		return projectionIssue(data, role, live), nil
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != role.ContentSHA256 {
		return "declared file content does not match content-sha256", nil
	}
	return "", nil
}
