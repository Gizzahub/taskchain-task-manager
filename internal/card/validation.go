package card

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"gopkg.in/yaml.v3"
)

type CriterionReport struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}
type ValidationFinding struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}
type ValidationReport struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	Scope           string              `json:"scope"`
	BoardValidation string              `json:"boardValidation"`
	Valid           bool                `json:"valid"`
	Criteria        []CriterionReport   `json:"criteria"`
	Findings        []ValidationFinding `json:"findings"`
}

var validationFilenameNumber = regexp.MustCompile(`^\d{2,3}[a-z]?-[a-z0-9-]+(?:\.\d{2})?\.md$`)

// ValidateCard validates a work-task card without executing commands or writing bytes.
func (d *Document) ValidateCard(path string, r ValidationRules) (ValidationReport, error) {
	report := ValidationReport{SchemaVersion: 1, Scope: "card", BoardValidation: "not_evaluated", Valid: true, Criteria: []CriterionReport{}, Findings: []ValidationFinding{}}
	fm, body, err := splitFrontmatter(d.raw)
	if err != nil {
		return report, err
	}
	var fields map[string]any
	if err := yaml.Unmarshal(fm, &fields); err != nil {
		return report, fmt.Errorf("parse card frontmatter: %w", err)
	}
	add := func(field, message string) {
		report.Valid = false
		report.Findings = append(report.Findings, ValidationFinding{Severity: "error", Field: field, Message: message})
	}
	if err := validateRules(r); err != nil {
		return report, err
	}
	stringField := func(field string) (string, bool) {
		v, ok := fields[field]
		if !ok {
			return "", false
		}
		s, ok := v.(string)
		return strings.TrimSpace(s), ok
	}
	id, idString := stringField("id")
	title, titleString := stringField("title")
	typ, typeString := stringField("type")
	priority, priorityString := stringField("priority")
	if r.IDRequired && !idString {
		add("id", "id is required")
	} else if !idString || id == "" {
		if _, present := fields["id"]; present {
			add("id", "id must be a nonempty string")
		}
	} else {
		parsed, e := cardid.Parse(id)
		if e != nil || parsed.Prefix != "TASK" {
			add("id", "id must be a canonical TASK numeric ID")
		}
	}
	if !titleString || title == "" {
		add("title", "title must be a nonempty string")
	}
	if !typeString || typ == "" {
		add("type", "type must be a nonempty string")
	} else if !contains(r.TaskTypes, typ) {
		add("type", fmt.Sprintf("type %q is not allowed", typ))
	}
	if !priorityString || priority == "" {
		add("priority", "priority must be a nonempty string")
	} else if !contains(r.PriorityValues, priority) {
		add("priority", fmt.Sprintf("priority %q is not allowed", priority))
	}
	criteria, summaryFound, criteriaFound, malformedCriteria := criteriaFromBody(string(body), r)
	report.Criteria = criteria
	if !summaryFound {
		add("summary-heading", fmt.Sprintf("missing h2 heading %q", r.SummaryHeading))
	}
	if !criteriaFound {
		add("criteria-heading", fmt.Sprintf("missing h2 heading %q", r.CriteriaHeading))
	}
	if criteriaFound && len(criteria) == 0 {
		add("criteria", "criteria section must contain at least one checkbox")
	}
	if malformedCriteria {
		add("criteria", "criteria checkbox must use [ ], [x], [X], or [>] followed by nonempty text")
	}
	if !filenameMatches(lastPath(path), r) {
		report.Findings = append(report.Findings, ValidationFinding{Severity: "warning", Field: "filename", Message: "filename is outside the CE card pattern"})
	}
	return report, nil
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func lastPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func filenameMatches(name string, r ValidationRules) bool {
	if validationFilenameNumber.MatchString(name) {
		return true
	}
	for _, prefix := range r.FilenamePrefixes {
		if regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `-[a-z0-9-]+(?:\.\d{2})?\.md$`).MatchString(name) {
			return true
		}
	}
	return false
}

func criteriaFromBody(body string, r ValidationRules) ([]CriterionReport, bool, bool, bool) {
	return criteriaFromBodyMode(body, r, false)
}

var checkboxLike = regexp.MustCompile(`^(?:(?:[-+*]|[0-9]+[.)])[ \t]*)?\[`)
var listMarker = regexp.MustCompile(`^(?:[-+*]|[0-9]+[.)])[ \t]+`)

func criteriaFromBodyMode(body string, r ValidationRules, strictCompletion bool) ([]CriterionReport, bool, bool, bool) {
	lines := strings.Split(body, "\n")
	var fence fenceState
	criteria := false
	foundSummary, foundCriteria := false, false
	malformed := false
	inList, blankAfterList := false, false
	out := []CriterionReport{}
	for _, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent >= 4 || strings.HasPrefix(line[indent:], "\t") {
			if strictCompletion && inList && strings.TrimSpace(line) != "" {
				blankAfterList = false
			}
			if strictCompletion && criteria && fence.length == 0 && inList && checkboxLike.MatchString(strings.TrimSpace(line)) {
				malformed = true // Could be a nested task, not an independent code block.
			}
			continue // Indented code is neither structure nor a fence delimiter.
		}
		line = line[indent:]
		if fence.consume(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if line == "#" || strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "#\t") {
			criteria = false
			inList, blankAfterList = false, false
			continue
		}
		if line == "##" || strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "##\t") {
			inList, blankAfterList = false, false
			heading := strings.TrimSpace(line[2:])
			if i := strings.LastIndex(heading, " #"); i >= 0 && strings.Trim(heading[i:], " #") == "" {
				heading = strings.TrimSpace(heading[:i])
			}
			lower := strings.ToLower(heading)
			wantSummary := strings.ToLower(strings.TrimSpace(r.SummaryHeading))
			wantCriteria := strings.ToLower(strings.TrimSpace(r.CriteriaHeading))
			criteria = lower == wantCriteria
			if lower == wantSummary {
				foundSummary = true
			}
			if criteria {
				foundCriteria = true
			}
			continue
		}
		if criteria {
			if strictCompletion {
				if trimmed == "" {
					blankAfterList = true
					continue
				}
				isList := listMarker.MatchString(trimmed)
				if blankAfterList && indent == 0 && !isList {
					inList = false
				}
				inList = inList || isList
				blankAfterList = false
			}
			if len(trimmed) >= 6 && strings.HasPrefix(trimmed, "- [") && trimmed[4] == ']' && (trimmed[3] == ' ' || trimmed[3] == 'x' || trimmed[3] == 'X' || trimmed[3] == '>') && (trimmed[5] == ' ' || trimmed[5] == '\t') && strings.TrimSpace(trimmed[6:]) != "" {
				out = append(out, CriterionReport{Text: strings.TrimSpace(trimmed[5:]), Checked: trimmed[3] == 'x' || trimmed[3] == 'X'})
			} else if strings.HasPrefix(trimmed, "- [") || (strictCompletion && checkboxLike.MatchString(trimmed)) {
				malformed = true
			}
		}
	}
	return out, foundSummary, foundCriteria, malformed
}
