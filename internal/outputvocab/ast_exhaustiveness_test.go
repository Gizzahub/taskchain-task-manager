package outputvocab_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

// TestConstDeclarationsMatchAllFuncs closes a gap the hand-typed spelling
// table and its All<X>() cross-check cannot see: a constant added to
// outputvocab.go next to its siblings, but never added to the All<X>()
// slice below it. checkSpelling only walks the hand-typed table;
// assertNoDuplicates only walks All<X>(); their cross-check compares those
// two to each other. Neither ever looks at what outputvocab.go itself
// declares. This test does: it parses the source file with go/parser and
// go/ast -- a witness independent of both the spelling table and of
// reflection over the compiled package -- and asserts that the constant
// values declared for each vocabulary type equal what that type's All<X>()
// actually returns.
func TestConstDeclarationsMatchAllFuncs(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "outputvocab.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing outputvocab.go: %v", err)
	}

	// declared collects, per named vocabulary type, the string values of
	// every constant declared with that explicit type in a `const (...)`
	// block, in source order.
	declared := map[string][]string{}

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || valueSpec.Type == nil {
				continue
			}
			typeIdent, ok := valueSpec.Type.(*ast.Ident)
			if !ok {
				continue
			}
			for _, value := range valueSpec.Values {
				lit, ok := value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquoting constant literal %s: %v", lit.Value, err)
				}
				declared[typeIdent.Name] = append(declared[typeIdent.Name], unquoted)
			}
		}
	}

	// want maps each vocabulary type name, as it is spelled in the source,
	// to the string values its own All<X>() function reports. This map is
	// hand-written on purpose: it is a second, independent listing of "what
	// this package's vocabularies are", not derived from the same source
	// the parser above already reads.
	want := map[string][]string{
		"ValidationState":     allStrings(outputvocab.AllValidationStates()),
		"ResultStatus":        allStrings(outputvocab.AllResultStatuses()),
		"RejoinPhase":         allStrings(outputvocab.AllRejoinPhases()),
		"SharedPhase":         allStrings(outputvocab.AllSharedPhases()),
		"RejoinMode":          allStrings(outputvocab.AllRejoinModes()),
		"ArchiveOperation":    allStrings(outputvocab.AllArchiveOperations()),
		"Zone":                allStrings(outputvocab.AllZones()),
		"Status":              allStrings(outputvocab.AllStatuses()),
		"Scope":               allStrings(outputvocab.AllScopes()),
		"ContextStatus":       allStrings(outputvocab.AllContextStatuses()),
		"ReferenceCheckState": allStrings(outputvocab.AllReferenceCheckStates()),
		"AuthorityScope":      allStrings(outputvocab.AllAuthorityScopes()),
		"RejoinRole":          allStrings(outputvocab.AllRejoinRoles()),
		"Severity":            allStrings(outputvocab.AllSeverities()),
		"ClaimStatus":         allStrings(outputvocab.AllClaimStatuses()),
	}

	if len(declared) != len(want) {
		t.Errorf("outputvocab.go declares %d const-typed vocabularies, this test knows of %d -- a type was added to one list and not the other", len(declared), len(want))
	}

	for typeName, wantValues := range want {
		gotValues, ok := declared[typeName]
		if !ok {
			t.Errorf("%s: All%s() exists but outputvocab.go declares no const of type %s (or this test's type-name spelling is wrong)", typeName, pluralHint(typeName), typeName)
			continue
		}
		checkSameSet(t, typeName, gotValues, wantValues)
	}
	for typeName := range declared {
		if _, ok := want[typeName]; !ok {
			t.Errorf("%s: outputvocab.go declares const values of this type, but this test has no matching All<X>() entry for it -- update the `want` map in ast_exhaustiveness_test.go", typeName)
		}
	}
}

func pluralHint(typeName string) string {
	return typeName + "(s)"
}

func allStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// checkSameSet fails if got and want, treated as sets, disagree in either
// direction -- a value declared in the source but missing from All<X>(),
// or a value All<X>() reports that the source does not declare with this
// type. Comparing sets (not just lengths) is what catches a swap: one
// value added on one side, a different one dropped on the other, which
// would otherwise leave the counts equal and pass silently.
func checkSameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	inWant := make(map[string]bool, len(want))
	for _, v := range want {
		inWant[v] = true
	}
	inGot := make(map[string]bool, len(got))
	for _, v := range got {
		inGot[v] = true
	}
	for _, v := range got {
		if !inWant[v] {
			t.Errorf("%s: outputvocab.go declares constant %q but All%s() does not return it", label, v, pluralHint(label))
		}
	}
	for _, v := range want {
		if !inGot[v] {
			t.Errorf("%s: All%s() returns %q but outputvocab.go declares no matching const of type %s -- possible copy-paste drift", label, pluralHint(label), v, label)
		}
	}
}
