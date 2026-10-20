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
		for _, spec := range file.Imports {
			if imported, _ := strconv.Unquote(spec.Path.Value); !allowed[imported] {
				t.Errorf("%s imports %s", path, imported)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "time" && (sel.Sel.Name == "Now" || sel.Sel.Name == "Since" || sel.Sel.Name == "Until") {
				t.Errorf("%s reads the clock via time.%s", path, sel.Sel.Name)
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "filepath" && (sel.Sel.Name == "Abs" || sel.Sel.Name == "EvalSymlinks" || sel.Sel.Name == "Walk" || sel.Sel.Name == "WalkDir" || sel.Sel.Name == "Glob") {
				t.Errorf("%s touches the filesystem via filepath.%s", path, sel.Sel.Name)
			}
			return true
		})
	}
}
