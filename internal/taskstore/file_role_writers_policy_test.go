package taskstore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestFileRoleWritersLegacyPoliciesRemainNoop(t *testing.T) {
	t.Parallel()
	for index, d := range []boardpolicy.Declaration{{}, {Relocations: []boardpolicy.Transition{}}, {Modules: []string{}}} {
		p, err := boardpolicy.New(d)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := p.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprintf("schema%d", index+1), func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			bindWriterRoleFixture(t, dir, p)
			discoveryFile(t, dir, "README.md", "legacy documentation\r\n")
			discoveryFile(t, dir, "unexpected.bin", "legacy non-card")
			if _, err := Create(dir, CreateRequest{Title: "legacy create"}); err != nil {
				t.Fatal(err)
			}
			if _, err := Transition(dir, req); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(dir, policyFile))
			if err != nil || string(after) != string(canonical) {
				t.Fatalf("legacy policy changed: %q %v", after, err)
			}
			after, err = os.ReadFile(filepath.Join(dir, "README.md"))
			if err != nil || string(after) != "legacy documentation\r\n" {
				t.Fatalf("legacy README changed: %q %v", after, err)
			}
		})
	}
}

func TestFileRoleWritersDoNotExemptUnsafeOrUndeclaredGuidance(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"undeclared", "symlink", "directory"} {
		t.Run(scenario, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			role := boardpolicy.FileRoleV5{Path: "AGENTS.md", Kind: boardpolicy.FileRoleGuidance, Owner: "fixture", SourceRevision: "r1", ContentSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("instructions")))}
			roles := []boardpolicy.FileRoleV5{role}
			if scenario == "undeclared" {
				roles = []boardpolicy.FileRoleV5{}
			}
			p, err := boardpolicy.NewV5(boardpolicy.Declaration{Modules: []string{}}, roles)
			if err != nil {
				t.Fatal(err)
			}
			bindWriterRoleFixture(t, dir, p)
			path := filepath.Join(dir, "AGENTS.md")
			switch scenario {
			case "undeclared":
				discoveryFile(t, dir, "AGENTS.md", "instructions")
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "symlink":
				err = os.Symlink("todo/custom-name.md", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			if _, err := Create(dir, CreateRequest{Title: "blocked"}); err == nil {
				t.Fatal("create accepted unsafe or undeclared guidance")
			}
			if _, err := Transition(dir, req); err == nil {
				t.Fatal("transition accepted unsafe or undeclared guidance")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("rejected guidance changed board")
			}
		})
	}
}

func TestFileRoleWritersValidateDeclaredV5Content(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"guidance", "template", "projection-drift"} {
		t.Run(scenario, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			content := "synthetic instructions\r\n"
			role := boardpolicy.FileRoleV5{Path: "AGENTS.md", Kind: boardpolicy.FileRoleGuidance, Owner: "fixture", SourceRevision: "r1", ContentSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))}
			if scenario == "template" {
				role.Path, role.Kind = "TEMPLATE.md", boardpolicy.FileRoleTemplate
			}
			if scenario == "projection-drift" {
				role.Path, role.Kind, role.ContentSHA256, role.Format = "README.md", boardpolicy.FileRoleProjection, "", "live-card-paths-v1"
				content = "<!-- task-manager:projection-begin format=\"live-card-paths-v1\" owner=\"fixture\" source-revision=\"r1\" -->\n- todo/missing.md\n<!-- task-manager:projection-end format=\"live-card-paths-v1\" owner=\"fixture\" source-revision=\"r1\" -->\n"
			}
			p, err := boardpolicy.NewV5(boardpolicy.Declaration{Modules: []string{}}, []boardpolicy.FileRoleV5{role})
			if err != nil {
				t.Fatal(err)
			}
			bindWriterRoleFixture(t, dir, p)
			discoveryFile(t, dir, role.Path, content)
			if scenario != "projection-drift" {
				// Exact declared bytes permit ordinary writes without rewriting the role file.
				if _, err := Create(dir, CreateRequest{Title: "declared content"}); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(filepath.Join(dir, role.Path))
				if err != nil || string(after) != content {
					t.Fatalf("declared content changed: %q %v", after, err)
				}
				discoveryFile(t, dir, role.Path, content+"drift")
			}
			for operation, run := range map[string]func() error{
				"create":     func() error { _, err := Create(dir, CreateRequest{Title: "rejected"}); return err },
				"transition": func() error { _, err := Transition(dir, req); return err },
			} {
				before := boardBytes(t, dir)
				err := run()
				if err == nil || !strings.Contains(err.Error(), "file-role admission") {
					t.Fatalf("%s accepted %s: %v", operation, scenario, err)
				}
				if !reflect.DeepEqual(before, boardBytes(t, dir)) {
					t.Fatalf("%s changed rejected %s", operation, scenario)
				}
			}
		})
	}
}
