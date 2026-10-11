package taskflow

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestHandoffZoneSkillsFromConfigAndPolicy(t *testing.T) {
	root := t.TempDir()
	writeCards(t, root, "tasks/todo/001-high.md", cardDoc("TASK-1", "High", "P0", "S", ""))

	absent, err := SelectState(root, "todo-run", nil)
	if err != nil || absent.Skill != "" {
		t.Fatalf("absent skill=%q err=%v", absent.Skill, err)
	}
	var text bytes.Buffer
	RenderStateText(&text, absent)
	if strings.Contains(text.String(), "skill=") {
		t.Fatalf("absent map printed a skill: %s", text.String())
	}
	raw, err := json.Marshal(absent)
	if err != nil || bytes.Contains(raw, []byte(`"skill"`)) {
		t.Fatalf("absent skill key leaked: %s %v", raw, err)
	}

	if err := writeFileForTest(root, "ce-tasks.yaml", []byte("task-gate:\n  schema-version: 1\n")); err != nil {
		t.Fatal(err)
	}
	still, err := SelectState(root, "todo-run", nil)
	if err != nil || still.Skill != "" {
		t.Fatalf("config without map skill=%q err=%v", still.Skill, err)
	}

	if err := writeFileForTest(root, "ce-tasks.yaml", []byte("task-board:\n  zone-skills:\n    todo: todo-skill\n")); err != nil {
		t.Fatal(err)
	}
	got, err := SelectState(root, "todo-run", nil)
	if err != nil || got.Skill != "todo-skill" {
		t.Fatalf("config skill=%q err=%v", got.Skill, err)
	}
	text.Reset()
	RenderStateText(&text, got)
	if !strings.Contains(text.String(), "todo-run: selection-required (zone=todo, skill=todo-skill)\n") {
		t.Fatalf("text=%s", text.String())
	}
	raw, err = json.Marshal(got)
	if err != nil || !bytes.Contains(raw, []byte(`"skill":"todo-skill"`)) {
		t.Fatalf("json=%s %v", raw, err)
	}
	other, err := SelectState(root, "doing-resume", nil)
	if err != nil || other.Skill != "" {
		t.Fatalf("unmapped zone skill=%q err=%v", other.Skill, err)
	}

	if err := writeFileForTest(root, "ce-tasks.yaml", []byte("task-board:\n  zone-skills: {nope: x}\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectState(root, "todo-run", nil); err == nil || !strings.Contains(err.Error(), `unknown zone "nope"`) {
		t.Fatalf("malformed config: %v", err)
	}
}

func TestHandoffZoneSkillsFromPolicyWhenConfigMapIsAbsent(t *testing.T) {
	root := t.TempDir()
	writeCards(t, root, "tasks/todo/001-high.md", cardDoc("TASK-1", "High", "P0", "S", ""))
	policy, err := boardpolicy.Parse([]byte("schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills:\n    review: review-skill\n"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := policy.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/.task-manager-policy.json", canonical); err != nil {
		t.Fatal(err)
	}
	got, err := SelectState(root, "review-run", nil)
	if err != nil || got.Skill != "review-skill" {
		t.Fatalf("policy skill=%q err=%v", got.Skill, err)
	}
	plain, err := boardpolicy.Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/.task-manager-policy.json", plain); err != nil {
		t.Fatal(err)
	}
	again, err := SelectState(root, "review-run", nil)
	if err != nil || again.Skill != "" {
		t.Fatalf("policy without map skill=%q err=%v", again.Skill, err)
	}
	if err := writeFileForTest(root, "tasks/.task-manager-policy.json", []byte("{")); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectState(root, "todo-run", nil); err == nil || !strings.Contains(err.Error(), "task-board.zone-skills") {
		t.Fatalf("malformed policy: %v", err)
	}
}

func TestHandoffEmptyZoneSkillMapOmitsSkill(t *testing.T) {
	root := t.TempDir()
	writeCards(t, root, "tasks/todo/001-high.md", cardDoc("TASK-1", "High", "P0", "S", ""))
	if err := writeFileForTest(root, "ce-tasks.yaml", []byte("task-board:\n  zone-skills: {}\n")); err != nil {
		t.Fatal(err)
	}
	got, err := SelectState(root, "todo-run", nil)
	if err != nil || got.Skill != "" {
		t.Fatalf("empty map skill=%q err=%v", got.Skill, err)
	}
}

func TestHandoffConfigDeclaredCustomZone(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "ce-tasks.yaml", []byte("schema-version: 1\ncard-dialect:\n  zones: [manual]\ntask-board:\n  zone-skills: {manual: parked-skill}\n")); err != nil {
		t.Fatal(err)
	}
	skills, err := loadHandoffSkills(root)
	if err != nil || skills["manual"] != "parked-skill" {
		t.Fatalf("declared custom zone skills=%v err=%v", skills, err)
	}
}
