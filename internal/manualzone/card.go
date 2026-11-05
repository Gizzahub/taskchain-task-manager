package manualzone

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseCard reads the manual admission fields. It does not accept a scalar
// operator as a structured operator and does not rewrite the card.
func ParseCard(content string) (CardFields, error) {
	raw, ok := frontmatter(content)
	if !ok {
		return CardFields{}, errors.New("card has no frontmatter")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return CardFields{}, fmt.Errorf("card frontmatter: %w", err)
	}
	var fields CardFields
	id, ok := doc["id"].(string)
	if !ok || id == "" {
		return CardFields{}, errors.New("card id is required")
	}
	fields.ID = id
	if action, ok := doc["manual-action"].(string); ok {
		fields.ManualAction = action
	}
	switch operator := doc["operator"].(type) {
	case nil:
	case string:
		fields.OperatorScalar = true
	case map[string]any:
		if len(operator) != 2 {
			return CardFields{}, errors.New("operator must contain only kind and id")
		}
		kind, kindOK := operator["kind"].(string)
		ident, idOK := operator["id"].(string)
		if !kindOK || !idOK {
			return CardFields{}, errors.New("operator.kind and operator.id must be strings")
		}
		fields.OperatorKind = kind
		fields.OperatorID = ident
	default:
		return CardFields{}, errors.New("operator must be a structure")
	}
	switch receipt := doc["expected-receipt"].(type) {
	case nil:
	case map[string]any:
		if len(receipt) != 2 {
			return CardFields{}, errors.New("expected-receipt must contain only path and facts")
		}
		receiptPath, pathOK := receipt["path"].(string)
		facts, factsOK := receipt["facts"].(string)
		if !pathOK || !factsOK {
			return CardFields{}, errors.New("expected-receipt path and facts must be strings")
		}
		fields.ReceiptPath = receiptPath
		fields.ReceiptFacts = facts
	default:
		return CardFields{}, errors.New("expected-receipt must be a structure")
	}
	return fields, nil
}

func frontmatter(content string) ([]byte, bool) {
	// CRLF is only a parse view. Digests and stamps stay on the original bytes.
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, false
	}
	rest := normalized[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, false
	}
	return []byte(rest[:end]), true
}
