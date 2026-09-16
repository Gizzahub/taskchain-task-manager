package card

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const validationConfigLimit = 64 * 1024

// ValidationRules is the work-card dialect used by ValidateCard.
type ValidationRules struct {
	Name             string
	IDRequired       bool
	SummaryHeading   string
	CriteriaHeading  string
	PriorityValues   []string
	TaskTypes        []string
	FilenamePrefixes []string
}

func defaultValidationRules() ValidationRules {
	return ValidationRules{
		Name: "ce", IDRequired: true, SummaryHeading: "Summary", CriteriaHeading: "Completion Criteria",
		PriorityValues:   []string{"P0", "P1", "P2", "P3"},
		TaskTypes:        []string{"feature", "bug", "chore", "refactor", "cleanup", "docs", "test"},
		FilenamePrefixes: []string{"P0", "P1", "P2", "P3"},
	}
}

// ParseValidationConfig parses the explicit versioned card-dialect envelope.
func ParseValidationConfig(raw []byte) (ValidationRules, error) {
	if len(raw) > validationConfigLimit {
		return ValidationRules{}, fmt.Errorf("validation config exceeds %d bytes", validationConfigLimit)
	}
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&root); err != nil {
		return ValidationRules{}, fmt.Errorf("parse validation config: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return ValidationRules{}, fmt.Errorf("validation config must contain one YAML document")
		}
		return ValidationRules{}, fmt.Errorf("parse validation config: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return ValidationRules{}, fmt.Errorf("validation config must be a mapping")
	}
	top := root.Content[0]
	if err := validateUniqueKeys(top, "config"); err != nil {
		return ValidationRules{}, err
	}
	var schema, dialect *yaml.Node
	for i := 0; i < len(top.Content); i += 2 {
		switch top.Content[i].Value {
		case "schema-version":
			schema = top.Content[i+1]
		case "card-dialect":
			dialect = top.Content[i+1]
		default:
			return ValidationRules{}, fmt.Errorf("unknown validation config field %q", top.Content[i].Value)
		}
	}
	if schema == nil || schema.Kind != yaml.ScalarNode || schema.Tag != "!!int" || schema.Value != "1" {
		return ValidationRules{}, fmt.Errorf("schema-version: required value is 1")
	}
	if dialect == nil || dialect.Kind != yaml.MappingNode {
		return ValidationRules{}, fmt.Errorf("card-dialect: required mapping")
	}
	if err := validateUniqueKeys(dialect, "card-dialect"); err != nil {
		return ValidationRules{}, err
	}
	for i := 0; i < len(dialect.Content); i += 2 {
		key, value := dialect.Content[i].Value, dialect.Content[i+1]
		if value.Kind == yaml.ScalarNode && value.Tag == "!!null" {
			return ValidationRules{}, fmt.Errorf("card-dialect.%s: null is not allowed", key)
		}
		if (key == "name" || key == "summary-heading" || key == "criteria-heading") && value.Kind == yaml.ScalarNode && strings.TrimSpace(value.Value) == "" {
			return ValidationRules{}, fmt.Errorf("card-dialect.%s: empty value is not allowed", key)
		}
		if key == "id-required" && (value.Kind != yaml.ScalarNode || value.Tag != "!!bool") {
			return ValidationRules{}, fmt.Errorf("card-dialect.id-required must be boolean")
		}
		if key == "name" || key == "summary-heading" || key == "criteria-heading" {
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return ValidationRules{}, fmt.Errorf("card-dialect.%s must be a string", key)
			}
		}
		if key == "priority-values" || key == "task-types" || key == "filename-prefixes" {
			if value.Kind != yaml.SequenceNode {
				return ValidationRules{}, fmt.Errorf("card-dialect.%s must be a sequence", key)
			}
			for _, item := range value.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
					return ValidationRules{}, fmt.Errorf("card-dialect.%s entries must be strings", key)
				}
			}
		}
	}
	var rules struct {
		Name             string   `yaml:"name"`
		IDRequired       *bool    `yaml:"id-required"`
		SummaryHeading   string   `yaml:"summary-heading"`
		CriteriaHeading  string   `yaml:"criteria-heading"`
		PriorityValues   []string `yaml:"priority-values"`
		TaskTypes        []string `yaml:"task-types"`
		FilenamePrefixes []string `yaml:"filename-prefixes"`
	}
	encoded, err := yaml.Marshal(dialect)
	if err != nil {
		return ValidationRules{}, fmt.Errorf("encode card-dialect: %w", err)
	}
	d := yaml.NewDecoder(bytes.NewReader(encoded))
	d.KnownFields(true)
	if err := d.Decode(&rules); err != nil {
		return ValidationRules{}, fmt.Errorf("decode card-dialect: %w", err)
	}
	out := defaultValidationRules()
	if rules.Name != "" {
		out.Name = rules.Name
	}
	if rules.IDRequired != nil {
		out.IDRequired = *rules.IDRequired
	}
	if rules.SummaryHeading != "" {
		out.SummaryHeading = rules.SummaryHeading
	}
	if rules.CriteriaHeading != "" {
		out.CriteriaHeading = rules.CriteriaHeading
	}
	if rules.PriorityValues != nil {
		out.PriorityValues = append([]string(nil), rules.PriorityValues...)
	}
	if rules.TaskTypes != nil {
		out.TaskTypes = append([]string(nil), rules.TaskTypes...)
	}
	if rules.FilenamePrefixes != nil {
		out.FilenamePrefixes = append([]string(nil), rules.FilenamePrefixes...)
	}
	if err := validateRules(out); err != nil {
		return ValidationRules{}, err
	}
	return out, nil
}

func validateUniqueKeys(n *yaml.Node, where string) error {
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Kind != yaml.ScalarNode || n.Content[i].Tag != "!!str" || n.Content[i].Value == "<<" {
			return fmt.Errorf("%s: keys must be ordinary strings", where)
		}
		k := n.Content[i].Value
		if seen[k] {
			return fmt.Errorf("%s: duplicate field %q", where, k)
		}
		seen[k] = true
	}
	return nil
}

func validateRules(r ValidationRules) error {
	for _, entry := range []struct{ name, value string }{
		{"name", r.Name}, {"summary-heading", r.SummaryHeading}, {"criteria-heading", r.CriteriaHeading},
	} {
		name, value := entry.name, entry.value
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("card-dialect.%s must be nonempty", name)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("card-dialect.%s must be a single line", name)
		}
	}
	for _, entry := range []struct {
		name   string
		values []string
	}{{"priority-values", r.PriorityValues}, {"task-types", r.TaskTypes}, {"filename-prefixes", r.FilenamePrefixes}} {
		name, values := entry.name, entry.values
		if len(values) == 0 {
			return fmt.Errorf("card-dialect.%s must be nonempty", name)
		}
		seen := map[string]bool{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("card-dialect.%s contains empty value", name)
			}
			if seen[value] {
				return fmt.Errorf("card-dialect.%s contains duplicate %q", name, value)
			}
			seen[value] = true
			if name == "filename-prefixes" && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`).MatchString(value) {
				return fmt.Errorf("card-dialect.filename-prefixes contains invalid %q", value)
			}
		}
	}
	if !contains([]string{"acceptance criteria", "completion criteria", "완료 조건", "완료 기준"}, strings.ToLower(strings.TrimSpace(r.CriteriaHeading))) {
		return fmt.Errorf("card-dialect.criteria-heading %q is not recognised", r.CriteriaHeading)
	}
	return nil
}
