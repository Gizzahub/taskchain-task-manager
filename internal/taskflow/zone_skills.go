package taskflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const (
	taskBoardConfigFile = "ce-tasks.yaml"
	boardPolicyFile     = ".task-manager-policy.json"
)

// loadHandoffSkills reads task-board.zone-skills. The config file wins when
// its map is present. Otherwise the activated board policy is used. An absent
// map returns nil and leaves the handoff skill empty.
func loadHandoffSkills(root string) (map[string]string, error) {
	skills, present, err := loadConfigZoneSkills(root)
	if err != nil {
		return nil, err
	}
	if present {
		return skills, nil
	}
	return loadPolicyZoneSkills(root)
}

func loadConfigZoneSkills(root string) (map[string]string, bool, error) {
	raw, err := os.ReadFile(filepath.Join(root, taskBoardConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", taskBoardConfigFile, err)
	}
	skills, present, err := boardpolicy.ParseTaskBoardConfig(raw)
	if err != nil {
		return nil, false, fmt.Errorf("task-board.zone-skills: %w", err)
	}
	return skills, present, nil
}

func loadPolicyZoneSkills(root string) (map[string]string, error) {
	path := filepath.Join(root, tasksDirName(), boardPolicyFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read board policy: %w", err)
	}
	policy, err := boardpolicy.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("task-board.zone-skills: %w", err)
	}
	if !policy.HasZoneSkills() {
		return nil, nil
	}
	return policy.ZoneSkills(), nil
}
