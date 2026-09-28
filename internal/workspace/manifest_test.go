package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, body string) []byte {
	t.Helper()
	for _, path := range []string{"alpha/board/nested", "beta/board", "beta/other"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return []byte(body)
}

func TestParseManifest(t *testing.T) {
	dir := t.TempDir()
	manifest, err := Parse(writeManifest(t, dir, `{"schemaVersion":1,"repositories":[{"name":"beta","path":"beta","board":"board"},{"name":"alpha","path":"alpha","board":"board"}]}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Repositories) != 2 || manifest.Repositories[0].Name != "alpha" || manifest.Repositories[1].Name != "beta" {
		t.Fatalf("repositories = %#v", manifest.Repositories)
	}
	physicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Repositories[0].Path != filepath.Join(physicalDir, "alpha") || manifest.Repositories[0].Board != filepath.Join(physicalDir, "alpha", "board") {
		t.Fatalf("resolved repository = %#v", manifest.Repositories[0])
	}
}

func TestParseRejectsInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	valid := `{"schemaVersion":1,"repositories":[{"name":"alpha","path":"alpha","board":"board"}]}`
	cases := map[string]string{
		"unknown":           strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"unknown":true`, 1),
		"duplicate key":     strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1),
		"empty repos":       `{"schemaVersion":1,"repositories":[]}`,
		"duplicate name":    `{"schemaVersion":1,"repositories":[{"name":"alpha","path":"alpha","board":"board"},{"name":"alpha","path":"beta","board":"board"}]}`,
		"bad name":          strings.Replace(valid, `"alpha"`, `"Alpha"`, 1),
		"absolute path":     strings.Replace(valid, `"path":"alpha"`, `"path":"/tmp"`, 1),
		"unclean path":      strings.Replace(valid, `"path":"alpha"`, `"path":"alpha/../alpha"`, 1),
		"backslash":         strings.Replace(valid, `"alpha"`, `"alpha\\board"`, 1),
		"parent board":      strings.Replace(valid, `"board"`, `"../board"`, 1),
		"trailing document": valid + ` {}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(writeManifest(t, dir, raw), dir); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestParseRejectsSymlinksAndOverlappingBoards(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "")
	if err := os.Symlink(filepath.Join(dir, "alpha"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"symlink component": `{"schemaVersion":1,"repositories":[{"name":"link","path":"link","board":"board"}]}`,
		"same board":        `{"schemaVersion":1,"repositories":[{"name":"alpha","path":"alpha","board":"board"},{"name":"beta","path":"alpha/board","board":"."}]}`,
		"overlapping board": `{"schemaVersion":1,"repositories":[{"name":"alpha","path":"alpha","board":"board"},{"name":"beta","path":"alpha/board","board":"nested"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw), dir); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}

func TestValidRelativePathRejectsBackslashAndControls(t *testing.T) {
	for _, path := range []string{"alpha\\beta", "alpha\x01beta", "alpha\nbeta"} {
		if validRelativePath(path) {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
}

func TestLoadRejectsManifestSymlinkAndOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"repositories":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("symlink manifest accepted")
	}
	if err := os.WriteFile(path, make([]byte, maxManifestBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}
