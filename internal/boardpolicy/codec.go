package boardpolicy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxPolicyBytes = 64 * 1024

// Parse decodes a strict versioned board-policy document.
func Parse(raw []byte) (Policy, error) {
	if len(raw) == 0 || len(raw) > maxPolicyBytes || !utf8.Valid(raw) {
		return Policy{}, errors.New("invalid board-policy size or UTF-8")
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return Policy{}, fmt.Errorf("parse board-policy: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Policy{}, errors.New("board-policy must contain one document")
		}
		return Policy{}, fmt.Errorf("parse board-policy: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return Policy{}, errors.New("board-policy root must be a mapping")
	}
	if err := validateNode(doc.Content[0], 0); err != nil {
		return Policy{}, err
	}
	root := doc.Content[0]
	if err := uniqueMapping(root, "root"); err != nil {
		return Policy{}, err
	}
	var schema, block *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		switch root.Content[i].Value {
		case "schema-version":
			schema = root.Content[i+1]
		case "board-policy":
			block = root.Content[i+1]
		default:
			return Policy{}, fmt.Errorf("unknown root field %q", root.Content[i].Value)
		}
	}
	if schema == nil || schema.Kind != yaml.ScalarNode || schema.Tag != "!!int" || (schema.Value != "1" && schema.Value != "2") {
		return Policy{}, errors.New("schema-version must be 1 or 2")
	}
	version := schema.Value[0] - '0'
	if block == nil || block.Kind != yaml.MappingNode {
		return Policy{}, errors.New("board-policy must be a mapping")
	}
	if err := uniqueMapping(block, "board-policy"); err != nil {
		return Policy{}, err
	}
	decl := Declaration{}
	if version == 2 {
		decl.Relocations = []Transition{}
		decl.KindStatus = map[string]string{}
	}
	for i := 0; i < len(block.Content); i += 2 {
		key, value := block.Content[i].Value, block.Content[i+1]
		switch key {
		case "zones":
			if value.Kind != yaml.SequenceNode {
				return Policy{}, errors.New("board-policy.zones must be a sequence")
			}
			for _, item := range value.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || item.Value == "" {
					return Policy{}, errors.New("board-policy.zones entries must be nonempty strings")
				}
				decl.Zones = append(decl.Zones, item.Value)
			}
		case "zone-status":
			if value.Kind != yaml.MappingNode {
				return Policy{}, errors.New("board-policy.zone-status must be a mapping")
			}
			if err := uniqueMapping(value, "board-policy.zone-status"); err != nil {
				return Policy{}, err
			}
			decl.ZoneStatus = map[string]string{}
			for j := 0; j < len(value.Content); j += 2 {
				k, v := value.Content[j], value.Content[j+1]
				if k.Tag != "!!str" || v.Kind != yaml.ScalarNode || v.Tag != "!!str" || k.Value == "" || v.Value == "" {
					return Policy{}, errors.New("board-policy.zone-status keys and values must be nonempty strings")
				}
				decl.ZoneStatus[k.Value] = v.Value
			}
		case "transitions":
			if value.Kind != yaml.SequenceNode {
				return Policy{}, errors.New("board-policy.transitions must be a sequence")
			}
			for _, item := range value.Content {
				if item.Kind != yaml.MappingNode {
					return Policy{}, errors.New("board-policy.transitions entries must be mappings")
				}
				if err := uniqueMapping(item, "board-policy.transitions"); err != nil {
					return Policy{}, err
				}
				var row Transition
				for j := 0; j < len(item.Content); j += 2 {
					k, v := item.Content[j], item.Content[j+1]
					switch k.Value {
					case "from":
						if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || v.Value == "" {
							return Policy{}, errors.New("transition.from must be a nonempty string")
						}
						row.From = v.Value
					case "to":
						if v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
							return Policy{}, errors.New("transition.to must be a nonempty sequence")
						}
						for _, target := range v.Content {
							if target.Kind != yaml.ScalarNode || target.Tag != "!!str" || target.Value == "" {
								return Policy{}, errors.New("transition.to entries must be nonempty strings")
							}
							row.To = append(row.To, target.Value)
						}
					default:
						return Policy{}, fmt.Errorf("unknown transition field %q", k.Value)
					}
				}
				if row.From == "" {
					return Policy{}, errors.New("transition.from is required")
				}
				decl.Transitions = append(decl.Transitions, row)
			}
		case "relocations":
			if version != 2 {
				return Policy{}, errors.New("relocations require schema-version 2")
			}
			rows, err := parseRelocations(value)
			if err != nil {
				return Policy{}, err
			}
			decl.Relocations = rows
		case "kind-status":
			if version != 2 {
				return Policy{}, errors.New("kind-status requires schema-version 2")
			}
			if value.Kind != yaml.MappingNode {
				return Policy{}, errors.New("board-policy.kind-status must be a mapping")
			}
			if err := uniqueMapping(value, "board-policy.kind-status"); err != nil {
				return Policy{}, err
			}
			for j := 0; j < len(value.Content); j += 2 {
				k, v := value.Content[j], value.Content[j+1]
				if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || v.Kind != yaml.ScalarNode || v.Tag != "!!str" || k.Value == "" || v.Value == "" || !isKindDir(k.Value) || !knownStatus(v.Value) {
					return Policy{}, errors.New("board-policy.kind-status contains an unsupported kind or status")
				}
				decl.KindStatus[k.Value] = v.Value
			}
		default:
			return Policy{}, fmt.Errorf("unknown board-policy field %q", key)
		}
	}
	policy, err := New(decl)
	if err != nil {
		return Policy{}, err
	}
	if _, err := policy.Canonical(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func parseRelocations(value *yaml.Node) ([]Transition, error) {
	if value.Kind != yaml.SequenceNode {
		return nil, errors.New("board-policy.relocations must be a sequence")
	}
	rows := []Transition{}
	for _, item := range value.Content {
		if item.Kind != yaml.MappingNode {
			return nil, errors.New("board-policy.relocations entries must be mappings")
		}
		if err := uniqueMapping(item, "board-policy.relocations"); err != nil {
			return nil, err
		}
		var row Transition
		for j := 0; j < len(item.Content); j += 2 {
			k, v := item.Content[j], item.Content[j+1]
			switch k.Value {
			case "from":
				if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || v.Value == "" {
					return nil, errors.New("relocation.from must be a nonempty string")
				}
				row.From = v.Value
			case "to":
				if v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
					return nil, errors.New("relocation.to must be a nonempty sequence")
				}
				for _, target := range v.Content {
					if target.Kind != yaml.ScalarNode || target.Tag != "!!str" || target.Value == "" {
						return nil, errors.New("relocation.to entries must be nonempty strings")
					}
					row.To = append(row.To, target.Value)
				}
			default:
				return nil, fmt.Errorf("unknown relocation field %q", k.Value)
			}
		}
		if row.From == "" {
			return nil, errors.New("relocation.from is required")
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func validateNode(n *yaml.Node, depth int) error {
	if depth > 16 {
		return errors.New("board-policy nesting exceeds 16 levels")
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return errors.New("board-policy anchors and aliases are not allowed")
	}
	validTag := (n.Kind == yaml.MappingNode && n.Tag == "!!map") ||
		(n.Kind == yaml.SequenceNode && n.Tag == "!!seq") ||
		(n.Kind == yaml.ScalarNode && (n.Tag == "!!str" || n.Tag == "!!int"))
	if !validTag {
		return fmt.Errorf("board-policy YAML tag %q does not match a supported value type", n.Tag)
	}
	for _, child := range n.Content {
		if err := validateNode(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func uniqueMapping(n *yaml.Node, where string) error {
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "" {
			return fmt.Errorf("%s keys must be nonempty strings", where)
		}
		if seen[key.Value] {
			return fmt.Errorf("%s has duplicate field %q", where, key.Value)
		}
		seen[key.Value] = true
	}
	return nil
}
