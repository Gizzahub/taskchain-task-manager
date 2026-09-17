package card

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"gopkg.in/yaml.v3"
)

// ArchiveFieldMapping names the exact top-level frontmatter fields an archive
// adapter wants to project. Empty names mean that the corresponding optional
// field is not mapped; there are no implicit CE field names.
type ArchiveFieldMapping struct {
	QualityReview         string
	QualityReviewEvidence string
	Resolution            string
	PromotedTo            string
	Children              string
}

// ArchiveMetadata is a typed, read-only projection of mapped archive metadata.
// Nil scalar pointers and nil slices mean that the mapped field was absent;
// present empty lists are represented by non-nil empty slices.
type ArchiveMetadata struct {
	QualityReview         *string
	QualityReviewEvidence *string
	Resolution            *string
	PromotedTo            []string
	Children              []string
}

// ProjectArchiveMetadata reads only the explicitly mapped top-level fields.
// It never rewrites or normalizes Document.Bytes, and performs no archive or
// completion judgement.
func (d *Document) ProjectArchiveMetadata(mapping ArchiveFieldMapping) (ArchiveMetadata, error) {
	var out ArchiveMetadata
	fields, err := archiveFieldNodes(d.raw)
	if err != nil {
		return out, err
	}
	names := map[string]string{}
	for _, field := range []struct{ property, name string }{
		{"quality-review", mapping.QualityReview},
		{"quality-review-evidence", mapping.QualityReviewEvidence},
		{"resolution", mapping.Resolution},
		{"promoted-to", mapping.PromotedTo},
		{"children", mapping.Children},
	} {
		property, name := field.property, field.name
		if name == "" {
			continue
		}
		if strings.TrimSpace(name) != name || strings.ContainsAny(name, ":\r\n\x00") {
			return out, fmt.Errorf("archive field mapping %s has invalid top-level name %q", property, name)
		}
		if previous, exists := names[name]; exists {
			return out, fmt.Errorf("ambiguous archive field mapping %q for %s and %s", name, previous, property)
		}
		names[name] = property
	}
	if err := archiveScalarField(fields, mapping.QualityReview, "quality-review", &out.QualityReview); err != nil {
		return out, err
	}
	if err := archiveScalarField(fields, mapping.QualityReviewEvidence, "quality-review-evidence", &out.QualityReviewEvidence); err != nil {
		return out, err
	}
	if err := archiveScalarField(fields, mapping.Resolution, "resolution", &out.Resolution); err != nil {
		return out, err
	}
	if err := archiveReferenceList(fields, mapping.PromotedTo, "promoted-to", &out.PromotedTo); err != nil {
		return out, err
	}
	if err := archiveReferenceList(fields, mapping.Children, "children", &out.Children); err != nil {
		return out, err
	}
	return out, nil
}

func archiveFieldNodes(raw []byte) (map[string]*yaml.Node, error) {
	fm, _, err := splitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, fmt.Errorf("parse archive frontmatter: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("archive frontmatter must be a YAML mapping")
	}
	root := doc.Content[0]
	fields := make(map[string]*yaml.Node, len(root.Content)/2)
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "" {
			return nil, errors.New("archive frontmatter keys must be nonempty strings")
		}
		if _, exists := fields[key.Value]; exists {
			return nil, fmt.Errorf("duplicate archive frontmatter field %q", key.Value)
		}
		fields[key.Value] = value
	}
	return fields, nil
}

func archiveScalarField(fields map[string]*yaml.Node, name, property string, dst **string) error {
	if name == "" {
		return nil
	}
	node, exists := fields[name]
	if !exists {
		return nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("archive field %s mapped from %q must be a string scalar", property, name)
	}
	value := node.Value
	*dst = &value
	return nil
}

func archiveReferenceList(fields map[string]*yaml.Node, name, property string, dst *[]string) error {
	if name == "" {
		return nil
	}
	node, exists := fields[name]
	if !exists {
		return nil
	}
	var nodes []*yaml.Node
	switch node.Kind {
	case yaml.ScalarNode:
		nodes = []*yaml.Node{node}
	case yaml.SequenceNode:
		nodes = node.Content
	default:
		return fmt.Errorf("archive field %s mapped from %q must be a card ID or sequence of card IDs", property, name)
	}
	values := make([]string, 0, len(nodes))
	for i, item := range nodes {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || strings.TrimSpace(item.Value) == "" {
			return fmt.Errorf("archive field %s entry %d must be a card ID string", property, i)
		}
		value := strings.TrimSpace(item.Value)
		if _, err := cardid.Parse(value); err != nil {
			return fmt.Errorf("archive field %s entry %d: %w", property, i, err)
		}
		values = append(values, value)
	}
	*dst = values
	return nil
}
