// Package card provides a small, source-preserving task-card codec.
package card

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
	"gopkg.in/yaml.v3"
)

// View is the derived, intentionally small public projection of a card.
type View struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	Priority  string   `json:"priority"`
	DependsOn []string `json:"dependsOn"`
}

// Document owns the exact source bytes and a derived view. Bytes never
// serializes the view: unknown metadata and prose belong to the source.
type Document struct {
	raw             []byte
	view            View
	needsHuman      bool
	executionMode   string
	allowedPaths    []string
	hasAllowedPaths bool
}

// Parse validates a card's leading YAML frontmatter and returns a document
// retaining the original bytes. Unknown mapping keys are deliberately allowed.
func Parse(raw []byte) (*Document, error) {
	fm, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(fm, &node); err != nil {
		return nil, fmt.Errorf("parse card frontmatter: %w", err)
	}
	if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("card frontmatter must be a YAML mapping")
	}
	var metadata map[string]any
	if err := yaml.Unmarshal(fm, &metadata); err != nil {
		return nil, fmt.Errorf("decode card frontmatter: %w", err)
	}
	deps, err := fmStrings(metadata["depends-on"])
	if err != nil {
		return nil, fmt.Errorf("decode depends-on: %w", err)
	}
	needsHuman, err := frontmatterBool(node.Content[0], metadata, "needs-human")
	if err != nil {
		return nil, err
	}
	executionMode, err := frontmatterExecutionMode(node.Content[0])
	if err != nil {
		return nil, err
	}
	allowedPaths, hasAllowedPaths, err := frontmatterAllowedPaths(node.Content[0])
	if err != nil {
		return nil, err
	}
	d := &Document{raw: append([]byte(nil), raw...)}
	d.needsHuman = needsHuman
	d.executionMode = executionMode
	d.allowedPaths = allowedPaths
	d.hasAllowedPaths = hasAllowedPaths
	d.view = View{
		ID:    fmString(metadata["id"]),
		Title: fmString(metadata["title"]), Status: fmString(metadata["status"]),
		Priority: fmString(metadata["priority"]), DependsOn: deps,
	}
	if d.view.Title == "" {
		d.view.Title = bodyTitle(body)
	}
	return d, nil
}

// Snapshot derives a view with CE-compatible workflow-directory precedence.
func (d *Document) Snapshot(path string) View {
	v := d.view
	if zone := zoneFromPath(path); zone != "" {
		v.Status = zone
	}
	v.DependsOn = append([]string(nil), v.DependsOn...)
	return v
}

// View returns a defensive copy of the parsed projection.
func (d *Document) View() View { return d.Snapshot("") }

// NeedsHuman reports whether this card is explicitly reserved for a human.
// Parse rejects a declared value that is not a YAML boolean, so callers never
// mistake malformed metadata for agent-runnable work.
func (d *Document) NeedsHuman() bool { return d.needsHuman }

// ExecutionMode reports how the card may be executed. It defaults to
// implementation when execution-mode is omitted.
func (d *Document) ExecutionMode() string { return d.executionMode }

// AllowedPaths reports the declared allowed-paths value and whether it was
// present in the card frontmatter. The returned slice is safe for callers to
// modify.
func (d *Document) AllowedPaths() ([]string, bool) {
	if !d.hasAllowedPaths {
		return nil, false
	}
	paths := make([]string, len(d.allowedPaths))
	copy(paths, d.allowedPaths)
	return paths, true
}

// Bytes returns a defensive copy of the exact input bytes.
func (d *Document) Bytes() []byte { return append([]byte(nil), d.raw...) }

var statusCell = regexp.MustCompile(`^([ \t]*\|[ \t]*\*\*Status\*\*[ \t]*\|[ \t]*)([^|\r\n]*)`)

// SetStatusCell patches only the first body Status cell outside fenced code.
// It does not add a missing cell and preserves all unrelated source bytes.
func (d *Document) SetStatusCell(status string) ([]byte, bool, error) {
	label, ok := statusLabel(status)
	if !ok {
		return nil, false, fmt.Errorf("invalid card status %q", status)
	}
	_, bodyStart, body, err := splitFrontmatterWithOffset(d.raw)
	if err != nil {
		return nil, false, err
	}
	lines := bytes.SplitAfter(body, []byte("\n"))
	var fence fenceState
	offset := bodyStart
	for _, line := range lines {
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if fence.consume(text) || !statusCell.Match(line) {
			offset += len(line)
			continue
		}
		parts := statusCell.FindSubmatch(line)
		loc := statusCell.FindIndex(line)
		if len(parts) < 2 || len(loc) < 2 {
			offset += len(line)
			continue
		}
		replacement := append([]byte{}, parts[1]...)
		value := string(parts[2])
		tail := ""
		if i := strings.Index(value, " — "); i >= 0 {
			tail = strings.TrimRight(value[i:], " \t")
		}
		replacement = append(replacement, label...)
		replacement = append(replacement, tail...)
		replacement = append(replacement, []byte(" ")...)
		out := make([]byte, 0, len(d.raw))
		out = append(out, d.raw[:offset]...)
		out = append(out, line[:loc[0]]...)
		out = append(out, replacement...)
		out = append(out, line[loc[1]:]...)
		out = append(out, d.raw[offset+len(line):]...)
		if bytes.Equal(out, d.raw) {
			return out, false, nil
		}
		return out, true, nil
	}
	return d.Bytes(), false, nil
}

func splitFrontmatter(raw []byte) ([]byte, []byte, error) {
	fm, _, body, err := splitFrontmatterWithOffset(raw)
	return fm, body, err
}

func splitFrontmatterWithOffset(raw []byte) ([]byte, int, []byte, error) {
	if !bytes.HasPrefix(raw, []byte("---\n")) && !bytes.HasPrefix(raw, []byte("---\r\n")) {
		return nil, 0, raw, errors.New("card frontmatter is missing")
	}
	lineEnd := bytes.IndexByte(raw, '\n')
	openingEnd := lineEnd + 1
	for lineEnd >= 0 {
		next := lineEnd + 1
		end := bytes.IndexByte(raw[next:], '\n')
		lineStart := next
		if end < 0 {
			end = len(raw)
		} else {
			end += next
		}
		line := bytes.TrimSuffix(raw[lineStart:end], []byte("\r"))
		if bytes.Equal(line, []byte("---")) {
			bodyStart := end
			if end < len(raw) {
				bodyStart++
			}
			return raw[openingEnd:lineStart], bodyStart, raw[bodyStart:], nil
		}
		if end == len(raw) {
			break
		}
		lineEnd = end
	}
	return nil, 0, nil, errors.New("card frontmatter is unterminated")
}

func fmString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func frontmatterBool(root *yaml.Node, metadata map[string]any, name string) (bool, error) {
	found := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, field := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value != name {
			continue
		}
		if found {
			return false, fmt.Errorf("duplicate card frontmatter field %q", name)
		}
		found = true
		if field.Kind != yaml.ScalarNode || field.Tag != "!!bool" {
			return false, fmt.Errorf("card frontmatter %q must be a YAML boolean", name)
		}
	}
	// The decoded mapping includes YAML merge keys. Inspecting only the direct
	// syntax above would classify an inherited needs-human: true as false.
	value, present := metadata[name]
	if !present {
		return false, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("card frontmatter %q must be a YAML boolean", name)
	}
	return boolean, nil
}

func frontmatterExecutionMode(root *yaml.Node) (string, error) {
	nodes, err := effectiveFrontmatterNodes(root, "execution-mode")
	if err != nil {
		return "", err
	}
	if len(nodes) == 0 {
		return "implementation", nil
	}
	var mode string
	for _, node := range nodes {
		value, err := executionModeValue(node)
		if err != nil {
			return "", err
		}
		if mode != "" && mode != value {
			return "", fmt.Errorf("conflicting effective card frontmatter field %q", "execution-mode")
		}
		mode = value
	}
	return mode, nil
}

func executionModeValue(node *yaml.Node) (string, error) {
	node = dereferenceYAMLNode(node)
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("card frontmatter %q must be one of implementation, external, decision", "execution-mode")
	}
	switch node.Value {
	case "implementation", "external", "decision":
		return node.Value, nil
	default:
		return "", fmt.Errorf("card frontmatter %q must be one of implementation, external, decision", "execution-mode")
	}
}

func frontmatterAllowedPaths(root *yaml.Node) ([]string, bool, error) {
	nodes, err := effectiveFrontmatterNodes(root, "allowed-paths")
	if err != nil {
		return nil, false, err
	}
	if len(nodes) == 0 {
		return nil, false, nil
	}
	var paths []string
	for _, node := range nodes {
		value, err := allowedPathsValue(node)
		if err != nil {
			return nil, false, err
		}
		if paths != nil && !reflect.DeepEqual(paths, value) {
			return nil, false, fmt.Errorf("conflicting effective card frontmatter field %q", "allowed-paths")
		}
		paths = value
	}
	copyPaths := make([]string, len(paths))
	copy(copyPaths, paths)
	return copyPaths, true, nil
}

func allowedPathsValue(node *yaml.Node) ([]string, error) {
	node = dereferenceYAMLNode(node)
	if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
		return nil, fmt.Errorf("card frontmatter %q must be a YAML sequence of nonempty strings", "allowed-paths")
	}
	paths := make([]string, 0, len(node.Content))
	for i, item := range node.Content {
		item = dereferenceYAMLNode(item)
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || strings.TrimSpace(item.Value) == "" {
			return nil, fmt.Errorf("card frontmatter %q item %d must be a nonempty string", "allowed-paths", i)
		}
		paths = append(paths, item.Value)
	}
	return paths, nil
}

// effectiveFrontmatterNodes returns the values that would apply through YAML
// merges. It retains direct and inherited values so strict fields can reject
// ambiguity rather than silently accepting yaml.v3's merge precedence.
func effectiveFrontmatterNodes(mapping *yaml.Node, name string) ([]*yaml.Node, error) {
	mapping = dereferenceYAMLNode(mapping)
	if mapping.Kind != yaml.MappingNode {
		return nil, errors.New("card frontmatter must be a YAML mapping")
	}
	var direct []*yaml.Node
	var merges []*yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case name:
			direct = append(direct, value)
		case "<<":
			merges = append(merges, value)
		}
	}
	if len(direct) > 1 {
		return nil, fmt.Errorf("duplicate card frontmatter field %q", name)
	}
	inherited := make([]*yaml.Node, 0, len(direct))
	inherited = append(inherited, direct...)
	for _, merge := range merges {
		values, err := mergedFrontmatterNodes(merge, name)
		if err != nil {
			return nil, err
		}
		inherited = append(inherited, values...)
	}
	return inherited, nil
}

func mergedFrontmatterNodes(node *yaml.Node, name string) ([]*yaml.Node, error) {
	node = dereferenceYAMLNode(node)
	switch node.Kind {
	case yaml.MappingNode:
		return effectiveFrontmatterNodes(node, name)
	case yaml.SequenceNode:
		var values []*yaml.Node
		for _, item := range node.Content {
			item = dereferenceYAMLNode(item)
			if item.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("malformed YAML merge for card frontmatter field %q", name)
			}
			inherited, err := effectiveFrontmatterNodes(item, name)
			if err != nil {
				return nil, err
			}
			values = append(values, inherited...)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("malformed YAML merge for card frontmatter field %q", name)
	}
}

func dereferenceYAMLNode(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

func fmStrings(v any) ([]string, error) {
	var out []string
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []any:
		for i, item := range x {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("item %d must be a nonempty string", i)
			}
			out = append(out, strings.TrimSpace(s))
		}
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil, errors.New("dependency must be a nonempty string")
		}
		out = []string{s}
	default:
		return nil, errors.New("expected a string or list of strings")
	}
	return out, nil
}

func bodyTitle(body []byte) string {
	var fence fenceState
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if fence.consume(line) {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func zoneFromPath(path string) string {
	parts := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	if len(parts) > 0 {
		parts = parts[:len(parts)-1] // the final segment is the card filename
	}
	for _, part := range parts {
		// A card under archive/done is stored, not done, and one under
		// plan/done is a plan, not a done task. These directories hold cards
		// whose status they do not express, and nothing below them names a
		// zone either, so stop rather than keep looking for one.
		if cardpath.IsStatusOpaqueZone(part) {
			return ""
		}
		if status := cardpath.WorkflowStatus(part); status != "" {
			return status
		}
	}
	return ""
}

func statusLabel(status string) ([]byte, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "todo":
		return []byte("[ ] Pending"), true
	case "in-progress", "in_progress", "doing":
		return []byte("[~] In Progress"), true
	case "review":
		return []byte("[>] Review"), true
	case "done":
		return []byte("[x] Done"), true
	case "blocked":
		return []byte("[!] Blocked"), true
	case "cancelled", "canceled":
		return []byte("[-] Cancelled"), true
	default:
		return nil, false
	}
}

type fenceState struct {
	marker byte
	length int
}

func (f *fenceState) consume(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "\\`") || strings.HasPrefix(trimmed, "\\~") {
		return f.length > 0
	}
	if f.length > 0 {
		n := 0
		for n < len(trimmed) && trimmed[n] == f.marker {
			n++
		}
		rest := strings.TrimSpace(trimmed[n:])
		if n >= f.length && rest == "" {
			f.length, f.marker = 0, 0
		}
		return true
	}
	if len(trimmed) == 0 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return false
	}
	n := 0
	marker := trimmed[0]
	for n < len(trimmed) && trimmed[n] == marker {
		n++
	}
	if n >= 3 {
		if marker == '`' && strings.Contains(trimmed[n:], "`") {
			return false
		}
		f.marker, f.length = marker, n
		return true
	}
	return false
}

// MarshalJSON keeps accidental exposure of source bytes out of API payloads.
func (d *Document) MarshalJSON() ([]byte, error) { return json.Marshal(d.View()) }
