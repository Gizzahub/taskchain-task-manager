package card

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetFrontmatterStatus replaces the scalar frontmatter status without
// reserializing the document. It is intentionally narrow: superseded is the
// only status this source-preserving terminal patch currently admits.
func (d *Document) SetFrontmatterStatus(status string) ([]byte, bool, error) {
	if status != "superseded" {
		return nil, false, fmt.Errorf("unsupported frontmatter status %q", status)
	}
	fm, _, _, err := splitFrontmatterWithOffset(d.raw)
	if err != nil {
		return nil, false, err
	}
	// YAML also recognizes standalone CR and Unicode line breaks. This patcher
	// indexes LF/CRLF source lines only; never mix the two coordinate systems.
	if bytes.Contains(bytes.ReplaceAll(fm, []byte("\r\n"), nil), []byte("\r")) ||
		strings.ContainsAny(string(fm), "\u0085\u2028\u2029") {
		return nil, false, errors.New("frontmatter status requires LF or CRLF line endings")
	}
	fmStart := 4
	if bytes.HasPrefix(d.raw, []byte("---\r\n")) {
		fmStart = 5
	}
	closeStart := fmStart + len(fm)
	var root yaml.Node
	if err := yaml.Unmarshal(fm, &root); err != nil {
		return nil, false, fmt.Errorf("parse card frontmatter: %w", err)
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode || root.Content[0].Style != 0 || root.Content[0].Anchor != "" {
		return nil, false, errors.New("frontmatter status requires a block mapping")
	}
	mapping := root.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Anchor != "" {
			return nil, false, errors.New("frontmatter contains an unsupported complex key")
		}
		if key.Value != "status" {
			continue
		}
		if value.Kind != yaml.ScalarNode || value.Anchor != "" || value.Style&yaml.TaggedStyle != 0 || value.Line != key.Line {
			return nil, false, errors.New("frontmatter status must be a single-line scalar")
		}
		if value.Tag != "!!str" || (value.Style != 0 && value.Style != yaml.SingleQuotedStyle && value.Style != yaml.DoubleQuotedStyle) {
			return nil, false, errors.New("frontmatter status must be a string scalar")
		}
		start, end, quote, err := statusValueSpan(fm, key.Line, key.Column, value.Value)
		if err != nil {
			return nil, false, err
		}
		if value.Value == status {
			return d.Bytes(), false, nil
		}
		replacement := []byte("superseded")
		if quote != 0 {
			replacement = append([]byte{quote}, append(replacement, quote)...)
		}
		out := make([]byte, 0, len(d.raw)+len(replacement)-(end-start))
		out = append(out, d.raw[:fmStart+start]...)
		out = append(out, replacement...)
		out = append(out, d.raw[fmStart+end:]...)
		if err := validateSupersededOutput(out); err != nil {
			return nil, false, err
		}
		return out, true, nil
	}

	lineEnding := []byte("\n")
	if bytes.Contains(fm, []byte("\r\n")) {
		lineEnding = []byte("\r\n")
	}
	indent := 0
	if len(mapping.Content) > 0 && mapping.Content[0].Column > 0 {
		indent = mapping.Content[0].Column - 1
	}
	insert := append(bytes.Repeat([]byte{' '}, indent), []byte("status: superseded")...)
	insert = append(insert, lineEnding...)
	if closeStart > 0 && d.raw[closeStart-1] != '\n' {
		insert = append(lineEnding, insert...)
	}
	out := make([]byte, 0, len(d.raw)+len(insert))
	out = append(out, d.raw[:closeStart]...)
	out = append(out, insert...)
	out = append(out, d.raw[closeStart:]...)
	if err := validateSupersededOutput(out); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func statusValueSpan(fm []byte, line, column int, decoded string) (int, int, byte, error) {
	lines := bytes.SplitAfter(fm, []byte("\n"))
	if line < 1 || line > len(lines) {
		return 0, 0, 0, errors.New("frontmatter status line is invalid")
	}
	lineBytes := bytes.TrimSuffix(bytes.TrimSuffix(lines[line-1], []byte("\n")), []byte("\r"))
	keyStart := column - 1
	if keyStart < 0 || keyStart >= len(lineBytes) {
		return 0, 0, 0, errors.New("frontmatter status key position is invalid")
	}
	colon := findMappingColon(lineBytes, keyStart)
	if colon < 0 {
		return 0, 0, 0, errors.New("frontmatter status mapping is ambiguous")
	}
	valueStart := colon + 1
	for valueStart < len(lineBytes) && (lineBytes[valueStart] == ' ' || lineBytes[valueStart] == '\t') {
		valueStart++
	}
	if valueStart == len(lineBytes) || lineBytes[valueStart] == '#' {
		return 0, 0, 0, errors.New("frontmatter status value is missing")
	}
	quote := byte(0)
	valueEnd := valueStart
	switch lineBytes[valueStart] {
	case '\'', '"':
		quote = lineBytes[valueStart]
		valueEnd = quotedEnd(lineBytes, valueStart, quote)
		if valueEnd < 0 {
			return 0, 0, 0, errors.New("multiline or unterminated frontmatter status")
		}
		if valueEnd < len(lineBytes) && !onlyComment(lineBytes[valueEnd:]) {
			return 0, 0, 0, errors.New("frontmatter status has unsupported trailing content")
		}
	default:
		if strings.ContainsAny(string(lineBytes[valueStart]), "[{*&|>!") {
			return 0, 0, 0, errors.New("frontmatter status uses an unsupported YAML construct")
		}
		valueEnd = plainEnd(lineBytes, valueStart)
	}
	lineStart := 0
	for i := 0; i < line-1; i++ {
		lineStart += len(lines[i])
	}
	if valueEnd <= valueStart || string(lineBytes[valueStart:valueEnd]) == "" || decoded == "" {
		return 0, 0, 0, errors.New("frontmatter status value is invalid")
	}
	var scanned string
	if err := yaml.Unmarshal(lineBytes[valueStart:valueEnd], &scanned); err != nil || scanned != decoded {
		return 0, 0, 0, errors.New("frontmatter status must be a single-line scalar")
	}
	return lineStart + valueStart, lineStart + valueEnd, quote, nil
}

func findMappingColon(line []byte, start int) int {
	var quote byte
	for i := start; i < len(line); i++ {
		if quote != 0 {
			if line[i] == quote {
				if quote == '\'' && i+1 < len(line) && line[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if line[i] == '\'' || line[i] == '"' {
			quote = line[i]
		} else if line[i] == ':' {
			return i
		}
	}
	return -1
}

func quotedEnd(line []byte, start int, quote byte) int {
	for i := start + 1; i < len(line); i++ {
		if quote == '"' && line[i] == '\\' {
			i++
			continue
		}
		if line[i] != quote {
			continue
		}
		if quote == '\'' && i+1 < len(line) && line[i+1] == quote {
			i++
			continue
		}
		return i + 1
	}
	return -1
}

func plainEnd(line []byte, start int) int {
	for i := start; i < len(line); i++ {
		if line[i] == '#' && (i == start || line[i-1] == ' ' || line[i-1] == '\t') {
			for i > start && (line[i-1] == ' ' || line[i-1] == '\t') {
				i--
			}
			return i
		}
	}
	end := len(line)
	for end > start && (line[end-1] == ' ' || line[end-1] == '\t') {
		end--
	}
	return end
}

func onlyComment(value []byte) bool {
	value = bytes.TrimSpace(value)
	return len(value) == 0 || value[0] == '#'
}

func validateSupersededOutput(raw []byte) error {
	doc, err := Parse(raw)
	if err != nil {
		return fmt.Errorf("validate frontmatter status patch: %w", err)
	}
	if doc.View().Status != "superseded" {
		return errors.New("frontmatter status patch did not produce superseded")
	}
	return nil
}
