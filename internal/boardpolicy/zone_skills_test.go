package boardpolicy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

func TestZoneSkillsAbsentPreservesCanonicalAndOmitsSkill(t *testing.T) {
	raw, err := Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.HasZoneSkills() || p.ZoneSkill("todo") != "" || p.ZoneSkills() != nil || p.taskBoardDeclared {
		t.Fatalf("absent map leaked: declared=%v skill=%q map=%v", p.HasZoneSkills(), p.ZoneSkill("todo"), p.ZoneSkills())
	}
	again, err := p.Canonical()
	if err != nil || string(again) != string(raw) || strings.Contains(string(again), "task-board") || strings.Contains(string(again), "skill") {
		t.Fatalf("canonical changed:\n%s", again)
	}
}

func TestZoneSkillsMapKnownZonesAndRoundTrip(t *testing.T) {
	var b strings.Builder
	b.WriteString("schema-version: 1\nboard-policy:\n  zones: [manual]\ntask-board:\n  zone-skills:\n")
	b.WriteString("    manual: park-skill\n")
	for _, zone := range outputvocab.AllZones() {
		b.WriteString("    " + string(zone) + ": skill-" + strings.ReplaceAll(string(zone), "_", "x") + "\n")
	}
	p, err := Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasZoneSkills() || p.ZoneSkill("todo") != "skill-todo" || p.ZoneSkill("manual") != "park-skill" || p.ZoneSkill("doing") == "" {
		t.Fatalf("skills=%v todo=%q manual=%q", p.ZoneSkills(), p.ZoneSkill("todo"), p.ZoneSkill("manual"))
	}
	canonical, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	q, err := Parse(canonical)
	if err != nil {
		t.Fatal(err)
	}
	again, err := q.Canonical()
	if err != nil || string(again) != string(canonical) {
		t.Fatalf("round trip\n%s\n%s\n%v", canonical, again, err)
	}
	bare, err := Default().Canonical()
	if err != nil || string(bare) == string(canonical) {
		t.Fatal("declared skills did not change canonical bytes")
	}
}

func TestZoneSkillsEmptyMapIsPresentAndOmitsLookup(t *testing.T) {
	p, err := Parse([]byte("schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasZoneSkills() || p.ZoneSkill("todo") != "" || len(p.ZoneSkills()) != 0 {
		t.Fatalf("empty map=%v skill=%q", p.ZoneSkills(), p.ZoneSkill("todo"))
	}
	raw, err := p.Canonical()
	if err != nil || !strings.Contains(string(raw), `"task-board":{"zone-skills":{}}`) {
		t.Fatalf("canonical=%s err=%v", raw, err)
	}
	sectionOnly, err := Parse([]byte("schema-version: 1\nboard-policy: {}\ntask-board: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sectionOnly.HasZoneSkills() || sectionOnly.ZoneSkill("review") != "" {
		t.Fatal("task-board without zone-skills declared a map")
	}
}

func TestZoneSkillsMalformedAndUnknownZones(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"unknown zone", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: {nope: skill}\n", `unknown zone "nope"`},
		{"undeclared parked", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: {manual: skill}\n", `unknown zone "manual"`},
		{"alias", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: {todos: skill}\n", `unknown zone "todos"`},
		{"empty skill", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: {todo: \"\"}\n", "invalid skill"},
		{"numeric skill", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: {todo: 1}\n", ""},
		{"null map", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: null\n", ""},
		{"sequence map", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: [todo]\n", ""},
		{"scalar block", "schema-version: 1\nboard-policy: {}\ntask-board: todo\n", ""},
		{"unknown field", "schema-version: 1\nboard-policy: {}\ntask-board:\n  other: {}\n", "unknown task-board field"},
		{"duplicate skill", "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills: {todo: a, todo: b}\n", "duplicate"},
		{"duplicate block", "schema-version: 1\nboard-policy: {}\ntask-board: {}\ntask-board: {}\n", "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.raw))
			if err == nil {
				t.Fatalf("accepted %s", tc.raw)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestZoneSkillAcceptsAnyNonEmptyString(t *testing.T) {
	for name, skill := range map[string]string{"single": "a", "space": " ", "padded": " skill", "long": strings.Repeat("a", 129)} {
		t.Run(name, func(t *testing.T) {
			raw := "schema-version: 1\nboard-policy: {}\ntask-board:\n  zone-skills:\n    todo: " + fmt.Sprintf("%q", skill) + "\n"
			p, err := Parse([]byte(raw))
			if err != nil || p.ZoneSkill("todo") != skill {
				t.Fatalf("skill %q rejected: %v %q", skill, err, p.ZoneSkill("todo"))
			}
		})
	}
}

func TestParseTaskBoardConfigIgnoresOtherKeys(t *testing.T) {
	skills, present, err := ParseTaskBoardConfig([]byte("task-gate:\n  schema-version: 1\ntask-maintenance:\n  policy-path: policy.md\n"))
	if err != nil || present || skills != nil {
		t.Fatalf("absent map present=%v skills=%v err=%v", present, skills, err)
	}
	skills, present, err = ParseTaskBoardConfig([]byte("task-board: {}\n"))
	if err != nil || present || skills != nil {
		t.Fatalf("section without map present=%v skills=%v err=%v", present, skills, err)
	}
	skills, present, err = ParseTaskBoardConfig([]byte("card-dialect:\n  zones: [review, todo]\ntask-board:\n  zone-skills:\n    review: review-skill\n"))
	if err != nil || !present || skills["review"] != "review-skill" || skills["todo"] != "" {
		t.Fatalf("config skills=%v present=%v err=%v", skills, present, err)
	}
	explicit, present, err := ParseTaskBoardConfig([]byte("card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: \"  todo-skill\"}}\n"))
	if err != nil || !present || explicit["todo"] != "  todo-skill" {
		t.Fatalf("explicit todo present=%v skills=%v err=%v", present, explicit, err)
	}
	if _, _, err = ParseTaskBoardConfig([]byte("task-board:\n  zone-skills:\n    todo: todo-skill\n")); err == nil || !strings.Contains(err.Error(), `undeclared zone "todo"`) {
		t.Fatalf("undeclared todo: %v", err)
	}
	if _, _, err = ParseTaskBoardConfig([]byte("card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {nope: x}}\n")); err == nil || !strings.Contains(err.Error(), `undeclared zone "nope"`) {
		t.Fatalf("config unknown zone: %v", err)
	}
	if _, _, err = ParseTaskBoardConfig([]byte("task-board: [\n")); err == nil {
		t.Fatal("malformed config accepted")
	}
	if _, _, err = ParseTaskBoardConfig([]byte("card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: \"\"}}\n")); err == nil {
		t.Fatal("empty config skill accepted")
	}
	ws, present, err := ParseTaskBoardConfig([]byte("card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: \"  \"}}\n"))
	if err != nil || !present || ws["todo"] != "  " {
		t.Fatalf("whitespace config skill present=%v skills=%v err=%v", present, ws, err)
	}
	for name, raw := range map[string]string{
		"duplicate":  "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: a, todo: b}}\n",
		"number":     "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: 1}}\n",
		"bool":       "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: true}}\n",
		"null-map":   "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: null}\n",
		"null-skill": "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: {todo: null}}\n",
		"sequence":   "card-dialect: {zones: [todo]}\ntask-board: {zone-skills: [todo]}\n",
	} {
		if _, _, err = ParseTaskBoardConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted %s config", name)
		}
	}
	empty, present, err := ParseTaskBoardConfig([]byte("task-board:\n  zone-skills: {}\n"))
	if err != nil || !present || len(empty) != 0 {
		t.Fatalf("empty config map present=%v skills=%v err=%v", present, empty, err)
	}
	if _, present, err = ParseTaskBoardConfig(nil); err != nil || present {
		t.Fatalf("empty body present=%v err=%v", present, err)
	}
	custom, present, err := ParseTaskBoardConfig([]byte("schema-version: 1\ncard-dialect:\n  zones: [manual]\ntask-board:\n  zone-skills: {manual: parked-skill}\n"))
	if err != nil || !present || custom["manual"] != "parked-skill" {
		t.Fatalf("declared config zone present=%v skills=%v err=%v", present, custom, err)
	}
}
