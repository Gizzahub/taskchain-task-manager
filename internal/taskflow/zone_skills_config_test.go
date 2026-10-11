package taskflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckConfigYAMLValidatesZoneSkillsWithoutRejectingOtherBlocks(t *testing.T) {
	root := t.TempDir()
	raw := "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: task.todo-resolve}}\ntask-gate: {schema-version: 1}\ntask-maintenance: {policy-path: policy.md}\n"
	if err := os.WriteFile(filepath.Join(root, "ce-tasks.yaml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfigYAML(root); err != nil {
		t.Fatal(err)
	}
}

func TestCheckConfigYAMLRejectsInvalidZoneSkills(t *testing.T) {
	for name, raw := range map[string]string{
		"undeclared": "card-dialect: {}\ntask-board: {zone-skills: {todo: task.todo}}\n",
		"empty":      "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: ''}}\n",
		"malformed":  "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: todo}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "ce-tasks.yaml"), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := CheckConfigYAML(root); err == nil {
				t.Fatal("accepted invalid zone-skills")
			}
		})
	}
}
