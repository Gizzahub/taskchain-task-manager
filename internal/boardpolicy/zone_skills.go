package boardpolicy

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
	"gopkg.in/yaml.v3"
)

// copyZoneSkills validates a declared task-board.zone-skills map. A nil
// result means the map was absent. An empty non-nil map was present and
// names no zone.
func copyZoneSkills(d Declaration) (map[string]string, error) {
	if !d.ZoneSkillsDeclared {
		return nil, nil
	}
	skills := make(map[string]string, len(d.ZoneSkills))
	for zone, skill := range d.ZoneSkills {
		if !zoneDeclared(zone, d.Zones) {
			return nil, fmt.Errorf("unknown zone %q", zone)
		}
		if !validZoneSkill(skill) {
			return nil, fmt.Errorf("invalid skill for zone %q", zone)
		}
		skills[zone] = skill
	}
	return skills, nil
}

func zoneDeclared(zone string, declared []string) bool {
	for _, known := range outputvocab.AllZones() {
		if string(known) == zone {
			return true
		}
	}
	if !validParkedName(zone) {
		return false
	}
	for _, name := range declared {
		if name == zone {
			return true
		}
	}
	return false
}

func validZoneSkill(skill string) bool {
	return skill != ""
}

// parseTaskBoardBlock reads one task-board mapping. The bool reports whether
// zone-skills itself was present. Other task-board fields are refused.
func parseTaskBoardBlock(block *yaml.Node, declared []string) (map[string]string, bool, error) {
	if block == nil || block.Kind != yaml.MappingNode || block.Tag != "!!map" {
		return nil, false, errors.New("task-board must be a mapping")
	}
	if err := uniqueMapping(block, "task-board"); err != nil {
		return nil, false, err
	}
	var skillsNode *yaml.Node
	for i := 0; i+1 < len(block.Content); i += 2 {
		switch block.Content[i].Value {
		case "zone-skills":
			skillsNode = block.Content[i+1]
		default:
			return nil, false, fmt.Errorf("unknown task-board field %q", block.Content[i].Value)
		}
	}
	if skillsNode == nil {
		return nil, false, nil
	}
	skills, err := parseZoneSkillMap(skillsNode, declared)
	if err != nil {
		return nil, false, err
	}
	return skills, true, nil
}

func parseZoneSkillMap(node *yaml.Node, declared []string) (map[string]string, error) {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return nil, errors.New("task-board.zone-skills must be a mapping")
	}
	if err := uniqueMapping(node, "task-board.zone-skills"); err != nil {
		return nil, err
	}
	skills := map[string]string{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		zone := node.Content[i].Value
		value := node.Content[i+1]
		if !zoneDeclared(zone, declared) {
			return nil, fmt.Errorf("unknown zone %q", zone)
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || !validZoneSkill(value.Value) {
			return nil, fmt.Errorf("invalid skill for zone %q", zone)
		}
		skills[zone] = value.Value
	}
	return skills, nil
}

// ParseTaskBoardConfig reads task-board.zone-skills from a config document.
// Keys other than task-board are ignored. A missing file body, a missing
// task-board section, or a missing zone-skills map returns (nil, false, nil).
// Malformed task-board values and unknown zones are refused.
func ParseTaskBoardConfig(raw []byte) (map[string]string, bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, false, fmt.Errorf("decode config: %w", err)
	}
	root := &doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) != 1 {
			return nil, false, errors.New("config must contain one document")
		}
		root = doc.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, false, nil
	}
	var block *yaml.Node
	seen := 0
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "task-board" {
			seen++
			block = root.Content[i+1]
		}
	}
	if seen > 1 {
		return nil, false, errors.New("task-board has duplicate field")
	}
	if block == nil {
		return nil, false, nil
	}
	if err := rejectTaskBoardAnchors(block); err != nil {
		return nil, false, err
	}
	declared, err := parseConfigDeclaredZones(root)
	if err != nil {
		return nil, false, err
	}
	return parseTaskBoardBlock(block, declared)
}

func parseConfigDeclaredZones(root *yaml.Node) ([]string, error) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "card-dialect" {
			continue
		}
		dialect := root.Content[i+1]
		if dialect.Kind != yaml.MappingNode {
			return nil, errors.New("card-dialect must be a mapping")
		}
		for j := 0; j+1 < len(dialect.Content); j += 2 {
			if dialect.Content[j].Value != "zones" {
				continue
			}
			zones := dialect.Content[j+1]
			if zones.Kind != yaml.SequenceNode {
				return nil, errors.New("card-dialect.zones must be a sequence")
			}
			declared := make([]string, 0, len(zones.Content))
			for _, zone := range zones.Content {
				if zone.Kind != yaml.ScalarNode || zone.Tag != "!!str" || zone.Value == "" {
					return nil, errors.New("card-dialect.zones entries must be nonempty strings")
				}
				declared = append(declared, zone.Value)
			}
			return declared, nil
		}
	}
	return nil, nil
}

func rejectTaskBoardAnchors(n *yaml.Node) error {
	if n.Anchor != "" || n.Kind == yaml.AliasNode {
		return errors.New("task-board anchors and aliases are not allowed")
	}
	for _, child := range n.Content {
		if err := rejectTaskBoardAnchors(child); err != nil {
			return err
		}
	}
	return nil
}
