package boardpolicy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseZoneSkills reads the optional task-board.zone-skills declaration from
// ce-tasks.yaml. The declaration is intentionally independent of Policy: a
// board-policy document remains byte-for-byte canonical when it has no
// zone-skill declaration.
func ParseZoneSkills(raw []byte) (map[string]string, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&doc); err != nil {
		// An empty file has no zone-skills map. Callers keep their previous output.
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("parse ce-tasks.yaml: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("ce-tasks.yaml must contain one document")
		}
		return nil, fmt.Errorf("parse ce-tasks.yaml: %w", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Tag == "!!null" {
		return nil, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("ce-tasks.yaml root must be a mapping")
	}
	root := doc.Content[0]
	if err := uniqueMapping(root, "ce-tasks.yaml"); err != nil {
		return nil, err
	}
	var board *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "task-board" {
			board = root.Content[i+1]
			break
		}
	}
	if board == nil {
		return nil, nil
	}
	if board.Kind != yaml.MappingNode {
		return nil, errors.New("task-board must be a mapping")
	}
	if err := uniqueMapping(board, "task-board"); err != nil {
		return nil, err
	}
	var skills *yaml.Node
	for i := 0; i < len(board.Content); i += 2 {
		switch board.Content[i].Value {
		case "zone-skills":
			skills = board.Content[i+1]
		}
	}
	if skills == nil {
		return nil, nil
	}
	declared, err := dialectZones(root)
	if err != nil {
		return nil, err
	}
	if skills.Kind != yaml.MappingNode {
		return nil, errors.New("task-board.zone-skills must be a mapping")
	}
	if err := uniqueMapping(skills, "task-board.zone-skills"); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(skills.Content)/2)
	for i := 0; i < len(skills.Content); i += 2 {
		zone, skill := skills.Content[i], skills.Content[i+1]
		if zone.Kind != yaml.ScalarNode || zone.Tag != "!!str" || strings.TrimSpace(zone.Value) == "" {
			return nil, errors.New("task-board.zone-skills keys must be nonempty strings")
		}
		if !declared[zone.Value] {
			return nil, fmt.Errorf("task-board.zone-skills names undeclared zone %q", zone.Value)
		}
		if skill.Kind != yaml.ScalarNode || skill.Tag != "!!str" || strings.TrimSpace(skill.Value) == "" {
			return nil, errors.New("task-board.zone-skills values must be nonempty strings")
		}
		result[zone.Value] = skill.Value
	}
	return result, nil
}

// dialectZones returns only names listed in card-dialect.zones.
// Workflow and kind directory names are not members unless that list includes them.
func dialectZones(root *yaml.Node) (map[string]bool, error) {
	zones := map[string]bool{}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "card-dialect" {
			continue
		}
		dialect := root.Content[i+1]
		if dialect.Kind != yaml.MappingNode {
			return nil, errors.New("card-dialect must be a mapping")
		}
		if err := uniqueMapping(dialect, "card-dialect"); err != nil {
			return nil, err
		}
		for j := 0; j < len(dialect.Content); j += 2 {
			if dialect.Content[j].Value != "zones" {
				continue
			}
			value := dialect.Content[j+1]
			if value.Kind != yaml.SequenceNode {
				return nil, errors.New("card-dialect.zones must be a sequence")
			}
			for _, item := range value.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || item.Value == "" {
					return nil, errors.New("card-dialect.zones entries must be nonempty strings")
				}
				zones[item.Value] = true
			}
		}
	}
	return zones, nil
}
