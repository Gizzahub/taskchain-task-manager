package roleprotocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const packagePath = "github.com/Gizzahub/taskchain-task-manager/internal/roleprotocol"

// TestPackageIsInactive enforces that no command, writer, gate, reader or
// runtime path reaches this package until §14.6/§14.7 are accepted: no Go
// file outside the package, test files included, may import it.
func TestPackageIsInactive(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found: %v", err)
	}
	self, _ := filepath.Abs(".")
	scanned := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "tmp", "build":
				return filepath.SkipDir
			}
			if path == self {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		scanned++
		for _, spec := range file.Imports {
			if imported, _ := strconv.Unquote(spec.Path.Value); imported == packagePath {
				t.Errorf("%s imports %s; the role protocol must stay inactive", path, packagePath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d Go files; the walk did not cover the module", scanned)
	}
}

// forbiddenCalls are the clock, timer and filesystem functions the allowed
// imports still expose, keyed by import path so an aliased import cannot hide
// them.
var forbiddenCalls = map[string]map[string]bool{
	"time": {"Now": true, "Since": true, "Until": true, "Sleep": true, "After": true, "Tick": true,
		"NewTimer": true, "NewTicker": true, "AfterFunc": true},
	"path/filepath": {"Abs": true, "EvalSymlinks": true, "Walk": true, "WalkDir": true, "Glob": true},
	"io/fs":         {"ReadFile": true, "ReadDir": true, "WalkDir": true, "Stat": true, "Glob": true, "Sub": true},
}

// TestPackageIsPure keeps the package free of I/O, process, network and clock
// access, so verification never becomes a side effect.
func TestPackageIsPure(t *testing.T) {
	allowed := map[string]bool{
		"bytes": true, "crypto/sha256": true, "encoding/base64": true, "encoding/binary": true,
		"encoding/hex": true, "errors": true, "fmt": true, "io/fs": true, "math": true,
		"path/filepath": true, "slices": true, "sort": true, "strconv": true, "strings": true,
		"time": true, "unicode/utf16": true, "unicode/utf8": true,
		"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy": true,
	}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range purityViolations(file, allowed) {
			t.Errorf("%s %s", path, call)
		}
	}
}

// purityViolations resolves each selector's package by import path, not by
// its local name, and refuses dot and blank imports that would hide calls.
func purityViolations(file *ast.File, allowed map[string]bool) []string {
	var out []string
	local := map[string]string{}
	for _, spec := range file.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		if !allowed[imported] {
			out = append(out, "imports "+imported)
		}
		name := imported[strings.LastIndex(imported, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." || name == "_" {
			out = append(out, "imports "+imported+" as "+name)
		}
		local[name] = imported
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// A local value that shadows an import name is flagged too; that
		// fails closed and the package declares none.
		if x, ok := sel.X.(*ast.Ident); ok && forbiddenCalls[local[x.Name]][sel.Sel.Name] {
			out = append(out, "calls "+local[x.Name]+"."+sel.Sel.Name)
		}
		return true
	})
	return out
}

func TestPurityCheckResolvesImports(t *testing.T) {
	src := `package p
import (
	clock "time"
	iofs "io/fs"
	"path/filepath"
)
func f() {
	clock.Sleep(1)
	_ = clock.After
	_, _ = iofs.ReadFile(nil, "x")
	_, _ = filepath.Abs("x")
	var t clock.Time
	_ = t.After(t)
}`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := purityViolations(file, map[string]bool{"time": true, "io/fs": true, "path/filepath": true})
	want := []string{"calls time.Sleep", "calls time.After", "calls io/fs.ReadFile", "calls path/filepath.Abs"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("violations %q, want %q", got, want)
	}
}
