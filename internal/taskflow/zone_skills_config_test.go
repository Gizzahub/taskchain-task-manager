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
	whitespace := t.TempDir()
	if err := os.WriteFile(filepath.Join(whitespace, "ce-tasks.yaml"), []byte("card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: '  '}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfigYAML(whitespace); err != nil {
		t.Fatal(err)
	}
	absent := t.TempDir()
	if err := os.WriteFile(filepath.Join(absent, "ce-tasks.yaml"), []byte("task-gate: {schema-version: 1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfigYAML(absent); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfigYAML(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckConfigYAMLRejectsInvalidZoneSkills(t *testing.T) {
	for name, raw := range map[string]string{
		"undeclared": "card-dialect: {}\ntask-board: {zone-skills: {todo: task.todo}}\n",
		"unknown":    "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {nope: task.todo}}\n",
		"empty":      "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: ''}}\n",
		"duplicate":  "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: a, todo: b}}\n",
		"number":     "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: 1}}\n",
		"null":       "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: null}\n",
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
