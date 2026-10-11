package boardpolicy

import (
	"bytes"
	"testing"
)

func TestParseZoneSkills(t *testing.T) {
	legacy := []byte("schema-version: 1\nboard-policy: {}\n")
	before, err := Parse(legacy)
	if err != nil {
		t.Fatal(err)
	}
	beforeCanonical, err := before.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	skills, err := ParseZoneSkills([]byte("card-dialect: {zones: [todo, manual]}\ntask-board:\n  zone-skills:\n    todo: task.todo-resolve\n    manual: task.manual\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := skills["todo"], "task.todo-resolve"; got != want || skills["manual"] != "task.manual" {
		t.Fatalf("skills=%v", skills)
	}
	after, err := Parse(legacy)
	if err != nil {
		t.Fatal(err)
	}
	afterCanonical, err := after.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeCanonical, afterCanonical) {
		t.Fatalf("legacy policy canonical changed: %s != %s", beforeCanonical, afterCanonical)
	}
	if skills, err := ParseZoneSkills([]byte("task-board: {presence: none}\n")); err != nil || skills != nil {
		t.Fatalf("absent skills=%v err=%v", skills, err)
	}
}

func TestParseZoneSkillsRejectsInvalidDeclarations(t *testing.T) {
	for name, raw := range map[string]string{
		"not mapping":       "task-board: {zone-skills: todo}\n",
		"unknown zone":      "task-board: {zone-skills: {todo: task.todo}}\n",
		"empty skill":       "task-board: {zone-skills: {todo: ''}}\n",
		"number skill":      "task-board: {zone-skills: {todo: 7}}\n",
		"duplicate zone":    "task-board: {zone-skills: {todo: a, todo: b}}\n",
		"bad dialect zones": "card-dialect: {zones: todo}\ntask-board: {zone-skills: {todo: a}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseZoneSkills([]byte(raw)); err == nil {
				t.Fatalf("accepted %q", raw)
			}
		})
	}
}
