package taskstore

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"gopkg.in/yaml.v3"
)

// CreateTemplate selects caller-supplied work-card rules for one creation.
type CreateTemplate struct {
	Rules    card.ValidationRules
	Type     string
	Priority string
	Summary  string
	Criteria []string
}

type configuredCard struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	Status    string   `yaml:"status"`
	DependsOn []string `yaml:"depends-on,omitempty"`
	Type      string   `yaml:"type"`
	Priority  string   `yaml:"priority"`
}

func validateConfiguredRequest(req CreateRequest) error {
	if req.Kind != "" && strings.ToLower(strings.TrimSpace(req.Kind)) != "task" {
		return errors.New("configured creation supports TASK cards only")
	}
	if req.ID != "" {
		id, err := cardid.Parse(req.ID)
		if err != nil || id.Prefix != "TASK" {
			return errors.New("configured creation ID must be TASK-N")
		}
	}
	if !validSingleLine(req.Title) {
		return errors.New("task title must be a nonempty single-line UTF-8 string")
	}
	if req.Template == nil {
		return errors.New("configured creation template is missing")
	}
	if !validSingleLine(req.Template.Type) || !validSingleLine(req.Template.Priority) || !validSingleLine(req.Template.Summary) {
		return errors.New("configured card fields must be nonempty single-line UTF-8 strings")
	}
	if len(req.Template.Criteria) == 0 {
		return errors.New("configured card requires at least one criterion")
	}
	for _, criterion := range req.Template.Criteria {
		if !validSingleLine(criterion) {
			return errors.New("criteria must be nonempty single-line UTF-8 strings")
		}
	}
	return nil
}

func validSingleLine(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00") && strings.TrimSpace(value) != ""
}

func renderConfigured(id string, req CreateRequest) ([]byte, error) {
	c := configuredCard{ID: id, Title: req.Title, Status: "pending", DependsOn: req.DependsOn, Type: req.Template.Type, Priority: req.Template.Priority}
	fm, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n# ")
	b.WriteString(req.Title)
	b.WriteString("\n\n## ")
	b.WriteString(req.Template.Rules.SummaryHeading)
	b.WriteString("\n\n")
	b.WriteString(escapeSummary(req.Template.Summary))
	b.WriteString("\n\n| **Status** | [ ] Pending |\n\n## ")
	b.WriteString(req.Template.Rules.CriteriaHeading)
	b.WriteString("\n\n")
	for _, criterion := range req.Template.Criteria {
		b.WriteString("- [ ] ")
		b.WriteString(criterion)
		b.WriteByte('\n')
	}
	raw := []byte(b.String())
	if len(raw) > 1<<20 {
		return nil, errors.New("generated card exceeds 1 MiB")
	}
	return raw, nil
}

func escapeSummary(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\\', '|', '#', '`', '~', '<', '>', '[', ']', '*':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validateConfiguredCard(id string, req CreateRequest) ([]byte, *card.Document, error) {
	raw, err := renderConfigured(id, req)
	if err != nil {
		return nil, nil, err
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse configured card: %w", err)
	}
	report, err := doc.ValidateCard("tasks/todo/"+id+".md", req.Template.Rules)
	if err != nil {
		return nil, nil, fmt.Errorf("validate configured card: %w", err)
	}
	if !report.Valid {
		return nil, nil, fmt.Errorf("configured card validation failed: %s", report.Findings[0].Message)
	}
	if len(report.Criteria) != len(req.Template.Criteria) {
		return nil, nil, errors.New("configured card did not preserve all criteria")
	}
	for i, criterion := range report.Criteria {
		if criterion.Checked || criterion.Text != strings.TrimSpace(req.Template.Criteria[i]) {
			return nil, nil, errors.New("configured card did not preserve unchecked criterion text")
		}
	}
	return raw, doc, nil
}
